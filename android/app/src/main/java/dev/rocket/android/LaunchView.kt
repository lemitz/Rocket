package dev.rocket.android

import android.app.Activity
import android.content.Context
import android.content.pm.ApplicationInfo
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.ColorFilter
import android.graphics.LinearGradient
import android.graphics.Matrix
import android.graphics.Paint
import android.graphics.PorterDuff
import android.graphics.PorterDuffColorFilter
import android.graphics.RadialGradient
import android.graphics.RectF
import android.graphics.Shader
import android.graphics.Typeface
import android.os.Build
import android.os.SystemClock
import android.text.TextPaint
import android.text.TextUtils
import android.util.Log
import android.view.HapticFeedbackConstants
import android.view.MotionEvent
import android.view.View
import android.view.WindowInsets
import kotlin.math.abs
import kotlin.math.cos
import kotlin.math.floor
import kotlin.math.hypot
import kotlin.math.max
import kotlin.math.min
import kotlin.math.pow
import kotlin.math.sin
import kotlin.random.Random

internal object Pal {
    val bg0 = 0xFF10111A.toInt()
    val bg2 = 0xFF2A2548.toInt()
    val white = 0xFFF5F6FF.toInt()
    val muted = 0xFFAEB0CE.toInt()
    val dim = 0xFF7E80A2.toInt()
    val purple = 0xFFA487EC.toInt()
    val blue = 0xFF5B8DEF.toInt()
    val good = 0xFF7CE3B8.toInt()
    val bad = 0xFFFF8D98.toInt()
    val warn = 0xFFFFC56B.toInt()
    val panel = 0xFF1C1D30.toInt()
}

/**
 * The whole main screen is one Canvas scene: the rocket, the single "choose files" button, the
 * slide-to-launch control, the flight and the result card. It owns no business logic; the
 * activity feeds it state and receives callbacks. The windows app draws the same scene.
 */
internal class LaunchView(context: Context) : View(context) {
    interface Host {
        fun onChoose()
        fun onClear()
        fun onTarget()
        fun onSettings()
        fun onLaunch()
        fun onCancel()
        fun onDismiss()
        fun onSheetPick(index: Int)
        fun onSheetClose()
    }

    enum class Phase { IDLE, LAUNCHING, FLYING, DEPARTING, OK, FAIL }

    var host: Host? = null
    var phase = Phase.IDLE
        private set

    private val d = resources.displayMetrics.density
    private val debug = context.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0
    private val paint = Paint(Paint.ANTI_ALIAS_FLAG)
    private val text = TextPaint(Paint.ANTI_ALIAS_FLAG)
    private val rect = RectF()
    private val drawMatrix = Matrix()
    private val rnd = Random(7)
    private val sprite: Bitmap = BitmapFactory.decodeResource(resources, R.drawable.rocket,
        BitmapFactory.Options().apply { inScaled = false })
    private var bgBitmap: Bitmap? = null

    // ---- state fed by the activity
    private var fileCount = 0
    private var selBytes = 0L
    private var selName: String? = null
    private var targetText = ""
    private var targetDot = Pal.warn
    private var targetPickable = false
    private var ready = false
    private var sheetItems: List<String> = emptyList()
    private var sheetSelected = -1
    private var sheetOpen = false
    private var progress = 0f
    private var incoming = false
    private var stats: TransferSnapshot? = null
    private var failText = ""
    private var topInset = 0f
    private var bottomInset = 0f

    // ---- animation state
    private var sliderPos = 0f
    private var dragging = false
    private var grab = 0f
    private var springing = false
    private var springFrom = 0f
    private var springAt = 0.0
    private var armed = false
    private var selA = 0f
    private var flightStart = 0.0
    private var departAt = 0.0
    private var failAt = 0.0
    private var returnAt = -1.0
    private var animUntil = 0.0
    private var flashAt = -10.0
    private var thrust = 0f
    private var streak = 0f
    private var scroll = 0f
    private var spawnAcc = 0f
    private var lastTick = 0.0
    private var displayProgress = 0f
    private var pressed = H_NONE
    private val parts = ArrayList<Particle>()

    // ---- frame-rate bookkeeping (kept from the earlier scene)
    private var requestingHighRate = false
    private var previousWindowRate = 0f
    private var previousDrawNanos = 0L
    private var diagnosticStartNanos = 0L
    private var diagnosticDraws = 0
    private var diagnosticIntervalNanos = 0L

    private class Particle(var x: Float, var y: Float, val vx: Float, val vy: Float, val life: Float,
                           val r0: Float, val r1: Float, val tone: Float) { var age = 0f }

    private val unitGlow = RadialGradient(0f, 0f, 1f,
        intArrayOf(-1, 0xE0FFFFFF.toInt(), 0x90FFFFFF.toInt(), 0x30FFFFFF.toInt(), 0),
        floatArrayOf(0f, .25f, .5f, .75f, 1f), Shader.TileMode.CLAMP)
    private val unitPuff = RadialGradient(0f, 0f, 1f,
        intArrayOf(-1, 0xF2FFFFFF.toInt(), 0xB3FFFFFF.toInt(), 0x59FFFFFF.toInt(), 0x1AFFFFFF.toInt(), 0),
        floatArrayOf(0f, .3f, .5f, .7f, .85f, 1f), Shader.TileMode.CLAMP)
    private val filters = HashMap<Int, ColorFilter>()
    private val glowPaint = Paint(Paint.ANTI_ALIAS_FLAG)

    init {
        contentDescription = context.getString(R.string.rocket_ready)
        isClickable = true
        setOnApplyWindowInsetsListener { _, insets ->
            val bars = insets.getInsets(WindowInsets.Type.systemBars())
            topInset = bars.top.toFloat()
            bottomInset = bars.bottom.toFloat()
            bgBitmap = null
            invalidate()
            insets
        }
    }

    // ================================================================ public API

    fun setSelection(count: Int, bytes: Long, name: String?) {
        fileCount = count; selBytes = bytes; selName = name
        if (count == 0) { sliderPos = 0f; springing = false }
        invalidate()
    }

    fun setTarget(label: String, dot: Int, pickable: Boolean, canLaunch: Boolean) {
        targetText = label; targetDot = dot; targetPickable = pickable; ready = canLaunch
        invalidate()
    }

    fun flashTarget() { flashAt = now(); animUntil = max(animUntil, flashAt + 1.0); invalidate() }

    fun openSheet(items: List<String>, selected: Int) {
        sheetItems = items; sheetSelected = selected; sheetOpen = items.isNotEmpty(); invalidate()
    }

    fun closeSheet() { sheetOpen = false; invalidate() }

    /** The rocket takes off; the controls fade. The transfer may still be connecting. */
    fun launch(isIncoming: Boolean) {
        incoming = isIncoming
        phase = Phase.LAUNCHING
        flightStart = now()
        displayProgress = 0f
        progress = 0f
        stats = null
        sheetOpen = false
        returnAt = -1.0
        contentDescription = context.getString(R.string.rocket_flying)
        updateFrameRate(true)
        postInvalidateOnAnimation()
    }

    /** [started] means the first bytes moved, i.e. the connection is up: the caption switches from "Connecting". */
    fun setProgress(fraction: Float, started: Boolean) {
        progress = fraction.coerceIn(0f, 1f)
        if (started && phase == Phase.LAUNCHING) phase = Phase.FLYING
    }

    fun succeed(snapshot: TransferSnapshot) {
        if (phase != Phase.LAUNCHING && phase != Phase.FLYING) return
        stats = snapshot
        departAt = now()
        phase = Phase.DEPARTING
        contentDescription = context.getString(R.string.rocket_finished, snapshot.completed,
            context.readableBytes(snapshot.bytes), context.readableTime(snapshot.elapsedNanos),
            context.readableSpeed(snapshot.bytesPerSecond.toLong()))
        performHapticFeedback(HapticFeedbackConstants.CONFIRM)
        postInvalidateOnAnimation()
    }

    fun fail(message: String) {
        if (phase == Phase.IDLE) flightStart = now() - 1.5
        failText = message
        failAt = now()
        phase = Phase.FAIL
        sliderPos = 0f
        contentDescription = message
        performHapticFeedback(HapticFeedbackConstants.REJECT)
        postInvalidateOnAnimation()
    }

    /** Back to the idle rocket (used after the result card is dismissed or a transfer is cancelled). */
    fun reset() {
        phase = Phase.IDLE
        stats = null
        sliderPos = 0f
        returnAt = now()
        animUntil = returnAt + 0.9
        contentDescription = context.getString(R.string.rocket_ready)
        invalidate()
    }

    // ================================================================ time and layout

    private fun now() = SystemClock.elapsedRealtimeNanos() / 1e9

    private class L {
        var u = 1f; var w = 0f; var h = 0f; var cx = 0f
        val gear = RectF(); val choose = RectF(); val summary = RectF(); val clear = RectF()
        val target = RectF(); val track = RectF(); val thumb = RectF()
        val card = RectF(); val ok = RectF(); val cancel = RectF(); val sheet = RectF()
        var minX = 0f; var travel = 0f
        var rocketCx = 0f; var rocketCy = 0f; var rocketW = 0f; var rocketH = 0f
        val rows = ArrayList<RectF>()
    }
    private val lay = L()

    private fun layout(): L {
        val l = lay
        val fw = width.toFloat(); val fh = height.toFloat()
        val u = min(fw / 410f, fh / 820f).coerceAtLeast(.6f)
        l.u = u; l.w = fw; l.h = fh; l.cx = fw / 2
        val base = fh - max(bottomInset, 0f) - 36 * u
        val colW = min(fw - 44 * u, 340 * u)
        l.gear.set(fw - 62 * u, topInset + 8 * u, fw - 14 * u, topInset + 56 * u)
        val cw = min(colW, 310 * u)
        l.choose.set(l.cx - cw / 2, base - 60 * u, l.cx + cw / 2, base)
        l.track.set(l.cx - colW / 2, base - 64 * u, l.cx + colW / 2, base)
        val th = 56 * u
        l.minX = l.track.left + 4 * u
        l.travel = l.track.width() - 8 * u - th
        val tx = l.minX + sliderPos * l.travel
        l.thumb.set(tx, l.track.top + 4 * u, tx + th, l.track.top + 4 * u + th)
        l.target.set(l.cx - colW / 2, l.track.top - 12 * u - 34 * u, l.cx + colW / 2, l.track.top - 12 * u)
        l.summary.set(l.cx - colW / 2, l.target.top - 10 * u - 48 * u, l.cx + colW / 2, l.target.top - 10 * u)
        l.clear.set(l.summary.right - 44 * u, l.summary.top + 2 * u, l.summary.right - 2 * u, l.summary.bottom - 2 * u)
        val rh = min(fh * .38f, fw * .80f / (SPRITE_W / SPRITE_H))
        l.rocketH = rh; l.rocketW = rh * SPRITE_W / SPRITE_H
        l.rocketCx = l.cx; l.rocketCy = topInset + (fh - topInset - max(bottomInset, 0f)) * .40f
        val cardW = min(fw - 40 * u, 372 * u); val cardH = 424 * u
        val cy = max(topInset + 24 * u, fh * .47f - cardH / 2)
        l.card.set(l.cx - cardW / 2, cy, l.cx + cardW / 2, cy + cardH)
        l.ok.set(l.card.left + 22 * u, l.card.bottom - 78 * u, l.card.right - 22 * u, l.card.bottom - 22 * u)
        l.cancel.set(l.cx - 80 * u, fh - max(bottomInset, 0f) - 68 * u, l.cx + 80 * u, fh - max(bottomInset, 0f) - 20 * u)
        l.rows.clear()
        if (sheetOpen) {
            val n = min(sheetItems.size, 5)
            val sh = n * 50 * u + 16 * u
            l.sheet.set(l.target.left, l.target.top - 8 * u - sh, l.target.right, l.target.top - 8 * u)
            for (i in 0 until n) l.rows.add(RectF(l.sheet.left + 8 * u, l.sheet.top + 8 * u + i * 50 * u,
                l.sheet.right - 8 * u, l.sheet.top + 8 * u + (i + 1) * 50 * u))
        }
        return l
    }

    private fun uiAlpha(t: Double): Float = when (phase) {
        Phase.IDLE -> if (returnAt < 0) 1f else ease((t - returnAt) / .5 - .4).toFloat()
        Phase.LAUNCHING, Phase.FLYING -> 1f - ease((t - flightStart) / .3).toFloat()
        else -> 0f
    }

    // ================================================================ simulation

    private fun tick(t: Double, l: L) {
        val dt = if (lastTick == 0.0) 1 / 60.0 else min(.05, t - lastTick)
        lastTick = t
        if (springing) {
            val k = ((t - springAt) / .32).toFloat()
            sliderPos = springFrom * (1 - ease(k.toDouble()).toFloat())
            if (k >= 1) { sliderPos = 0f; springing = false }
        }
        val selT = if (fileCount > 0) 1f else 0f
        selA = approach(selA, selT, (dt * 5).toFloat())
        displayProgress += (progress - displayProgress) * (1 - Math.exp(-6 * dt)).toFloat()
        if (phase == Phase.DEPARTING && t - departAt >= 2.4) phase = Phase.OK

        var thrustT = 0f; var streakT = 0f
        when (phase) {
            Phase.LAUNCHING, Phase.FLYING -> { thrustT = 1f; streakT = ((t - flightStart - .25) / 1.1).coerceIn(0.0, 1.0).toFloat() }
            Phase.DEPARTING -> if (t - departAt < .95) { thrustT = 1.7f; streakT = 2.6f }
            else -> {}
        }
        thrust = approach(thrust, thrustT, (dt * 3.5).toFloat())
        streak = approach(streak, streakT, (dt * 2.2).toFloat())
        scroll += streak * dt.toFloat()

        if (thrust > .12f && width > 0) {
            rocketState(l, t)
            val tip = flameTip(l)
            spawnAcc += dt.toFloat() * 170f * thrust
            while (spawnAcc >= 1f) { spawnAcc -= 1f; spawn(tip.first, tip.second, l.u) }
        }
        val it = parts.iterator()
        while (it.hasNext()) {
            val p = it.next()
            p.age += dt.toFloat()
            if (p.age >= p.life) { it.remove(); continue }
            p.x += p.vx * dt.toFloat(); p.y += p.vy * dt.toFloat()
        }
    }

    private fun spawn(x: Float, y: Float, u: Float) {
        if (parts.size > 260) return
        val speed = (190 + rnd.nextFloat() * 190) * u * (.7f + .3f * min(thrust, 1f))
        val spread = (rnd.nextFloat() - .5f) * 90 * u
        parts.add(Particle(x + (rnd.nextFloat() - .5f) * 8 * u, y + (rnd.nextFloat() - .5f) * 8 * u,
            -AXIS_X * speed - AXIS_Y * spread, -AXIS_Y * speed + AXIS_X * spread,
            .35f + rnd.nextFloat() * .5f, (3 + rnd.nextFloat() * 3) * u, (10 + rnd.nextFloat() * 10) * u, rnd.nextFloat()))
    }

    private var rx = 0f; private var ry = 0f; private var ralpha = 1f; private var rflame = 0f

    private fun rocketState(l: L, t: Double) {
        var dist = 0.0; var dy = 0.0; var shx = 0.0; var shy = 0.0
        ralpha = 1f; rflame = 0f
        val liftMax = l.h * .085
        fun hover(sec: Double) = easeInOut((sec - .15) / 1.2) * liftMax
        when (phase) {
            Phase.IDLE -> if (returnAt >= 0) {
                val r = ease((t - returnAt) / .6)
                ralpha = r.toFloat(); dy = (1 - r) * 36 * l.u
            }
            Phase.LAUNCHING, Phase.FLYING -> {
                val sec = t - flightStart
                dist = hover(sec)
                val amp = l.u * (2.2 * min(1.0, sec / .6) - 1.4 * easeInOut((sec - 1.2) / 1))
                shx = sin(sec * 71) * amp; shy = cos(sec * 93) * amp
                dy = sin(sec * 2.6) * 3 * l.u * easeInOut(sec - 1.2)
                rflame = 1f
            }
            Phase.DEPARTING -> {
                val sec = t - departAt
                val exit = hypot(l.w.toDouble(), l.h.toDouble()) * 1.15
                dist = hover(departAt - flightStart) + exit * (sec / .95).coerceIn(0.0, 1.0).pow(2.3)
                rflame = 1f
                if (sec > .95) ralpha = 0f
            }
            Phase.FAIL -> {
                val sec = t - failAt
                dist = hover(failAt - flightStart) * (1 - easeInOut(sec / .7))
                rflame = (1 - ease(sec / .3)).toFloat()
                ralpha = (1 - ease((sec - .05) / .35)).toFloat()
            }
            Phase.OK -> ralpha = 0f
        }
        rx = l.rocketCx + (AXIS_X * dist + shx).toFloat()
        ry = l.rocketCy + (AXIS_Y * dist + dy + shy).toFloat()
    }

    private fun flameTip(l: L): Pair<Float, Float> {
        val sc = l.rocketH / SPRITE_H
        return Pair(rx - l.rocketW / 2 + FLAME_X * sc, ry - l.rocketH / 2 + FLAME_Y * sc)
    }

    private fun animating(t: Double): Boolean = when (phase) {
        Phase.LAUNCHING, Phase.FLYING, Phase.DEPARTING -> true
        else -> dragging || springing || selA != (if (fileCount > 0) 1f else 0f) || t < animUntil ||
            parts.isNotEmpty() || thrust > .01f || streak > .01f
    }

    // ================================================================ drawing

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        super.onSizeChanged(w, h, oldw, oldh); bgBitmap = null
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        if (width == 0 || height == 0) return
        val t = now()
        val l = layout()
        tick(t, l)
        val u = l.u
        val backdrop = bgBitmap ?: buildBackground(l).also { bgBitmap = it }
        canvas.drawBitmap(backdrop, 0f, 0f, null)

        drawStreaks(canvas, l)
        for (p in parts) {
            val k = p.age / p.life
            val r = p.r0 + (p.r1 - p.r0) * k
            val c = lerpColor(0xFFFFFFFF.toInt(), lerpColor(Pal.purple, Pal.blue, p.tone), (k * 1.6f).coerceAtMost(1f))
            glow(canvas, p.x, p.y, r, c, (1 - k) * (1 - k) * .5f, unitGlow)
        }
        rocketState(l, t)
        if (ralpha > 0f) {
            val sc = l.rocketH / SPRITE_H
            val left = rx - l.rocketW / 2; val top = ry - l.rocketH / 2
            val tipX = left + FLAME_X * sc; val tipY = top + FLAME_Y * sc
            if (rflame > 0f) {
                val f = (.9 + .1 * sin(t * 83)).toFloat()
                glow(canvas, tipX, tipY, 120 * u * f * (.6f + .5f * thrust), 0xFF9A7BFF.toInt(), .55f * rflame * ralpha, unitGlow)
            }
            drawMatrix.setScale(sc, sc); drawMatrix.postTranslate(left, top)
            paint.alpha = (ralpha * 255).toInt(); paint.isFilterBitmap = true
            canvas.drawBitmap(sprite, drawMatrix, paint)
            paint.alpha = 255
            if (rflame > 0f) glow(canvas, tipX + 4 * u, tipY - 5 * u, 46 * u, 0xFFFFFFFF.toInt(), .5f * rflame * ralpha, unitGlow)
        }
        if (phase == Phase.DEPARTING || phase == Phase.OK) drawSmoke(canvas, l, t, false)
        val uiA = uiAlpha(t)
        when (phase) {
            Phase.IDLE -> drawIdle(canvas, l, t, uiA)
            Phase.LAUNCHING, Phase.FLYING -> drawFlightUi(canvas, l, t)
            Phase.DEPARTING -> {
                val dd = t - departAt
                drawCard(canvas, l, ease((dd - .9) / .55).toFloat(), (.94 + .06 * ease((dd - .9) / .55)).toFloat(), true)
                drawSmoke(canvas, l, t, true)
            }
            Phase.OK -> drawCard(canvas, l, 1f, 1f, true)
            Phase.FAIL -> {
                val f = t - failAt
                drawCard(canvas, l, ease((f - .25) / .4).toFloat(), (.95 + .05 * ease((f - .25) / .4)).toFloat(), false)
            }
        }
        if (sheetOpen && phase == Phase.IDLE) drawSheet(canvas, l)

        val active = animating(t)
        updateFrameRate(active)
        recordDraw(SystemClock.elapsedRealtimeNanos())
        if (active) postInvalidateOnAnimation() else lastTick = 0.0
    }

    private fun buildBackground(l: L): Bitmap {
        val bmp = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888)
        val c = Canvas(bmp)
        val fh = height.toFloat(); val fw = width.toFloat()
        val stops = floatArrayOf(0f, .25f, .5f, .75f, 1f)
        val colors = IntArray(5) { lerpColor(Pal.bg0, Pal.bg2, stops[it].pow(1.7f)) }
        paint.shader = LinearGradient(0f, 0f, 0f, fh, colors, stops, Shader.TileMode.CLAMP)
        paint.isDither = true
        c.drawRect(0f, 0f, fw, fh, paint)
        paint.shader = null
        glow(c, l.rocketCx + 20 * l.u, l.rocketCy, max(fw, fh) * .55f, 0xFF6C55D8.toInt(), .26f, unitGlow)
        glow(c, fw * .15f, fh * .78f, fw * .9f, 0xFF3E6AE0.toInt(), .10f, unitGlow)
        glow(c, fw * .9f, fh * .08f, fw * .5f, 0xFF8C6BE0.toInt(), .10f, unitGlow)
        val sr = Random(7)
        repeat(70 + (fw * fh / 60000f).toInt()) {
            val x = sr.nextFloat() * fw; val y = sr.nextFloat() * fh * .92f
            val r = (.5f + sr.nextFloat() * sr.nextFloat() * 1.3f) * l.u
            paint.color = if (sr.nextFloat() < .3f) Pal.purple else Pal.white
            paint.alpha = ((.18f + sr.nextFloat() * .6f) * 255).toInt()
            c.drawCircle(x, y, r, paint)
        }
        paint.alpha = 255
        return bmp
    }

    // Colours are quantised so particle tones share a small cache of filters.
    private fun filter(color: Int): ColorFilter {
        val key = (color and 0xFCFCFC) or 0x020202 or (0xFF shl 24)
        return filters.getOrPut(key) { PorterDuffColorFilter(key, PorterDuff.Mode.SRC_IN) }
    }

    private fun glow(c: Canvas, cx: Float, cy: Float, r: Float, color: Int, alpha: Float, shader: Shader) {
        if (alpha <= 0f || r <= 0f) return
        glowPaint.shader = shader
        glowPaint.colorFilter = filter(color)
        glowPaint.alpha = (alpha.coerceAtMost(1f) * 255).toInt()
        c.save(); c.translate(cx, cy); c.scale(r, r)
        c.drawCircle(0f, 0f, 1f, glowPaint)
        c.restore()
    }

    private fun drawStreaks(c: Canvas, l: L) {
        if (streak < .01f) return
        val diag = hypot(l.w, l.h); val track = diag * 1.15f
        val dx = -AXIS_X; val dy = -AXIS_Y; val px = -dy; val py = dx
        paint.strokeCap = Paint.Cap.ROUND; paint.style = Paint.Style.STROKE
        for (i in 0 until 38) {
            val seed = i * .6180339f + .13f
            val lane = frac(seed * 7.31f); val phase0 = frac(seed * 3.77f)
            val mult = .55f + frac(seed * 5.19f) * .9f
            val alpha = (.10f + frac(seed * 2.13f) * .30f) * min(1f, streak)
            val length = (26 + frac(seed * 9.7f) * 90) * l.u * (.35f + min(streak, 1.5f))
            val pos = frac(phase0 + scroll * mult * .55f * l.u / track * 60f)
            val hx = l.w / 2 + px * (lane - .5f) * diag + dx * (pos - .5f) * track
            val hy = l.h / 2 + py * (lane - .5f) * diag + dy * (pos - .5f) * track
            if (hx < -80 || hy < -80 || hx > l.w + 80 || hy > l.h + 80) continue
            paint.color = if (i % 3 == 0) Pal.purple else Pal.white
            paint.alpha = (alpha * 255).toInt()
            paint.strokeWidth = (.9f + frac(seed * 4.4f) * 1.5f) * l.u
            c.drawLine(hx, hy, hx - dx * length, hy - dy * length, paint)
        }
        paint.style = Paint.Style.FILL; paint.alpha = 255
    }

    private fun drawSmoke(c: Canvas, l: L, t: Double, front: Boolean) {
        var dd = t - departAt
        if (phase != Phase.DEPARTING) dd = 10.0
        if (dd < .5) return
        val ccx = l.card.centerX(); val ccy = l.card.centerY()
        if (front) {
            for (i in 0 until 8) {
                val seed = i * .754877f + .2f
                val fx = (ccx + (i - 3.5f) / 3.5f * l.card.width() * .52f).coerceIn(l.w * .06f, l.w * .94f)
                val fy = l.card.top + l.card.height() * (.78f + .13f * sin(i * 2.1f))
                val r = (52 + frac(seed * 8.1f) * 62) * l.u
                val p = ease((dd - .45 - .03 * i) / .7)
                val a = (.42 * p * (1 - ease((dd - 1.0) / .9))).toFloat()
                glow(c, fx, fy, r, lerpColor(0xFFD9D1FA.toInt(), 0xFF9A92DE.toInt(), frac(seed * 3.3f)), a, unitPuff)
            }
            return
        }
        for (i in 0 until 18) {
            val seed = i * .754877f + .2f
            val angle = frac(seed * 5.3f) * 2 * Math.PI
            val dist = .8f + .5f * frac(seed * 2.9f)
            val fx = ccx + cos(angle).toFloat() * l.card.width() * .46f * dist
            val fy = ccy + sin(angle).toFloat() * l.card.height() * .50f * dist + l.card.height() * .06f
            val r = (46 + frac(seed * 8.1f) * 56) * l.u
            if (fx < l.w * .1f || fx > l.w * .9f) continue // a puff cut by the screen edge would show up as a bright bar
            val p = ease((dd - .5 - .025 * i) / 1.2).toFloat()
            // Puffs squeezed against the screen edge would pile up into a bright bar: fade them out.
            val edge = (min(fx, l.w - fx) / (r * .8f)).coerceIn(0f, 1f)
            glow(c, fx, fy, r * (.6f + .4f * p), lerpColor(0xFFD9D1FA.toInt(), 0xFF8E86D6.toInt(), frac(seed * 3.3f)),
                (.12f + frac(seed * 6.7f) * .10f) * p * edge, unitPuff)
        }
    }

    // ---- text and shapes

    private fun txt(c: Canvas, s: String, x: Float, y: Float, size: Float, weight: Int, color: Int, alpha: Float,
                    align: Int, u: Float, maxWidth: Float = Float.MAX_VALUE) {
        if (alpha <= 0f || s.isEmpty()) return
        text.textSize = size * u
        text.typeface = Typeface.create(Typeface.DEFAULT, weight, false)
        text.color = color
        text.alpha = (alpha.coerceAtMost(1f) * 255).toInt()
        text.textAlign = when (align) { 1 -> Paint.Align.CENTER; 2 -> Paint.Align.RIGHT; else -> Paint.Align.LEFT }
        val shown = if (maxWidth < Float.MAX_VALUE) TextUtils.ellipsize(s, text, maxWidth, TextUtils.TruncateAt.END).toString() else s
        val fm = text.fontMetrics
        c.drawText(shown, x, y - (fm.ascent + fm.descent) / 2, text)
    }

    private fun pill(c: Canvas, r: RectF, color: Int, alpha: Float, radius: Float = r.height() / 2) {
        paint.color = color; paint.alpha = (alpha.coerceIn(0f, 1f) * 255).toInt()
        c.drawRoundRect(r, radius, radius, paint); paint.alpha = 255
    }

    private fun gradientPill(c: Canvas, r: RectF, a: Int, b: Int, alpha: Float, radius: Float = r.height() / 2) {
        paint.shader = LinearGradient(r.left, r.top, r.right, r.bottom, a, b, Shader.TileMode.CLAMP)
        paint.alpha = (alpha.coerceIn(0f, 1f) * 255).toInt()
        c.drawRoundRect(r, radius, radius, paint)
        paint.shader = null; paint.alpha = 255
    }

    private fun stroke(c: Canvas, r: RectF, color: Int, alpha: Float, width: Float, radius: Float = r.height() / 2) {
        paint.style = Paint.Style.STROKE; paint.strokeWidth = width
        paint.color = color; paint.alpha = (alpha.coerceIn(0f, 1f) * 255).toInt()
        rect.set(r.left + width / 2, r.top + width / 2, r.right - width / 2, r.bottom - width / 2)
        c.drawRoundRect(rect, radius, radius, paint)
        paint.style = Paint.Style.FILL; paint.alpha = 255
    }

    private fun line(c: Canvas, x0: Float, y0: Float, x1: Float, y1: Float, w: Float, color: Int, alpha: Float) {
        paint.style = Paint.Style.STROKE; paint.strokeCap = Paint.Cap.ROUND; paint.strokeWidth = w
        paint.color = color; paint.alpha = (alpha.coerceIn(0f, 1f) * 255).toInt()
        c.drawLine(x0, y0, x1, y1, paint)
        paint.style = Paint.Style.FILL; paint.alpha = 255
    }

    private fun disc(c: Canvas, cx: Float, cy: Float, r: Float, color: Int, alpha: Float) {
        paint.color = color; paint.alpha = (alpha.coerceIn(0f, 1f) * 255).toInt()
        c.drawCircle(cx, cy, r, paint); paint.alpha = 255
    }

    private fun primaryButton(c: Canvas, r: RectF, label: String, u: Float, id: Int, a: Float) {
        var p = Pal.purple; var q = Pal.blue
        if (pressed == id) { p = lerpColor(p, Pal.bg0, .18f); q = lerpColor(q, Pal.bg0, .18f) }
        glow(c, r.centerX(), r.top + r.height() * .7f, r.width() * .62f, Pal.purple, .22f * a, unitGlow)
        gradientPill(c, r, p, q, a)
        txt(c, label, r.centerX(), r.centerY(), 17.5f, 600, Pal.white, a, 1, u)
    }

    private fun drawIdle(c: Canvas, l: L, t: Double, a: Float) {
        if (a <= 0f) return
        val u = l.u
        // gear
        val gx = l.gear.centerX(); val gy = l.gear.centerY()
        val gc = if (pressed == H_GEAR) Pal.white else Pal.muted
        paint.style = Paint.Style.STROKE; paint.strokeWidth = 4.2f * u; paint.color = gc; paint.alpha = (a * 255).toInt()
        c.drawCircle(gx, gy, 6.2f * u, paint)
        paint.style = Paint.Style.FILL; paint.alpha = 255
        for (k in 0 until 8) {
            val ang = k * Math.PI / 4; val cs = cos(ang).toFloat(); val sn = sin(ang).toFloat()
            line(c, gx + cs * 8.6f * u, gy + sn * 8.6f * u, gx + cs * 10.6f * u, gy + sn * 10.6f * u, 3.6f * u, gc, a)
        }
        if (selA < 1f) {
            val bo = a * (1 - selA)
            primaryButton(c, l.choose, context.getString(R.string.choose_files), u, H_CHOOSE, bo)
        }
        if (selA > 0f) {
            val fa = a * selA
            drawSummary(c, l, fa)
            drawTargetChip(c, l, t, fa)
            drawSlider(c, l, fa)
        }
    }

    private fun summaryLabel(): String {
        val size = context.readableBytes(selBytes)
        return if (fileCount == 1 && selName != null) "$selName · $size" else "${context.fileCount(fileCount)} · $size"
    }

    private fun drawSummary(c: Canvas, l: L, a: Float) {
        val u = l.u; val r = l.summary
        pill(c, r, Pal.white, (if (pressed == H_SUMMARY) .13f else .08f) * a)
        stroke(c, r, Pal.white, .12f * a, u)
        val fx = r.left + 22 * u; val fy = r.centerY()
        rect.set(fx - 6 * u, fy - 8 * u, fx + 6 * u, fy + 8 * u)
        gradientPill(c, rect, Pal.purple, Pal.blue, a, 3 * u)
        txt(c, summaryLabel(), fx + 18 * u, fy, 16f, 600, Pal.white, a, 0, u, r.width() - 110 * u)
        val cx = l.clear.centerX(); val cy = l.clear.centerY()
        if (pressed == H_CLEAR) disc(c, cx, cy, 14 * u, Pal.white, .12f * a)
        val cc = if (pressed == H_CLEAR) Pal.white else Pal.muted
        line(c, cx - 4.5f * u, cy - 4.5f * u, cx + 4.5f * u, cy + 4.5f * u, 1.9f * u, cc, a)
        line(c, cx - 4.5f * u, cy + 4.5f * u, cx + 4.5f * u, cy - 4.5f * u, 1.9f * u, cc, a)
    }

    private fun drawTargetChip(c: Canvas, l: L, t: Double, a: Float) {
        val u = l.u; val r = l.target
        val pulse = (1 - (t - flashAt) / .9).coerceIn(0.0, 1.0).toFloat()
        pill(c, r, Pal.white, ((if (pressed == H_TARGET && targetPickable) .10f else .045f) + pulse * .05f) * a)
        if (pulse > 0f) stroke(c, r, Pal.warn, pulse * a, 1.4f * u)
        disc(c, r.left + 18 * u, r.centerY(), 3.6f * u, targetDot, a)
        txt(c, targetText, r.left + 32 * u, r.centerY(), 14f, 500, Pal.muted, a, 0, u, r.width() - 66 * u)
        if (targetPickable) {
            val cx = r.right - 20 * u; val cy = r.centerY()
            line(c, cx - 4.5f * u, cy - 2 * u, cx, cy + 2.6f * u, 1.7f * u, Pal.muted, a)
            line(c, cx, cy + 2.6f * u, cx + 4.5f * u, cy - 2 * u, 1.7f * u, Pal.muted, a)
        }
    }

    private fun drawSlider(c: Canvas, l: L, a: Float) {
        val u = l.u; val t = l.track
        pill(c, t, Pal.white, .07f * a)
        stroke(c, t, Pal.white, .12f * a, u)
        val fill = RectF(t.left, t.top, l.thumb.right + 4 * u, t.bottom)
        gradientPill(c, fill, Pal.purple, Pal.blue, a * (.25f + .6f * sliderPos) * (if (ready) 1f else .25f))
        val labelA = a * (1 - (sliderPos * 2.2f).coerceAtMost(1f))
        txt(c, context.getString(R.string.launch_label), t.centerX() + 16 * u, t.centerY(), 18.5f, 600,
            if (ready) Pal.white else Pal.dim, labelA, 1, u)
        val th = l.thumb; val cx = th.centerX(); val cy = th.centerY()
        var pa = Pal.purple; var pb = Pal.blue
        if (!ready) { pa = lerpColor(pa, Pal.dim, .55f); pb = lerpColor(pb, Pal.dim, .55f) }
        else if (dragging) { pa = lerpColor(pa, Pal.white, .15f); pb = lerpColor(pb, Pal.white, .15f) }
        if (ready) glow(c, cx, cy + 4 * u, 46 * u, Pal.purple, .4f * a, unitGlow)
        paint.shader = LinearGradient(th.left, th.top, th.right, th.bottom, pa, pb, Shader.TileMode.CLAMP)
        paint.alpha = (a * 255).toInt()
        c.drawCircle(cx, cy, th.width() / 2, paint)
        paint.shader = null; paint.alpha = 255
        for (k in 0 until 2) {
            val ox = cx - 5 * u + k * 9 * u
            line(c, ox - 2.6f * u, cy - 6.5f * u, ox + 2.6f * u, cy, 2.5f * u, Pal.white, a)
            line(c, ox + 2.6f * u, cy, ox - 2.6f * u, cy + 6.5f * u, 2.5f * u, Pal.white, a)
        }
    }

    private fun drawSheet(c: Canvas, l: L) {
        val u = l.u
        paint.color = Pal.bg0; paint.alpha = (.55f * 255).toInt(); c.drawRect(0f, 0f, l.w, l.h, paint); paint.alpha = 255
        pill(c, l.sheet, Pal.panel, .98f, 22 * u)
        stroke(c, l.sheet, Pal.white, .12f, u, 22 * u)
        for ((i, row) in l.rows.withIndex()) {
            if (pressed == H_PHONE + i) pill(c, row, Pal.white, .09f, 16 * u)
            txt(c, sheetItems[i], row.left + 18 * u, row.centerY(), 15.5f, 500, Pal.white, 1f, 0, u, row.width() - 70 * u)
            if (i == sheetSelected) {
                val cx = row.right - 24 * u; val cy = row.centerY()
                line(c, cx - 6 * u, cy, cx - 2 * u, cy + 4 * u, 2.2f * u, Pal.good, 1f)
                line(c, cx - 2 * u, cy + 4 * u, cx + 6 * u, cy - 4 * u, 2.2f * u, Pal.good, 1f)
            }
        }
    }

    private fun drawFlightUi(c: Canvas, l: L, t: Double) {
        val u = l.u
        val sec = t - flightStart
        val a = ease((sec - 1.0) / .6).toFloat()
        if (a <= 0f) return
        val caption = if (phase == Phase.LAUNCHING) context.getString(R.string.connecting)
        else {
            val verb = context.getString(if (incoming) R.string.receiving else R.string.sending)
            if (progress > 0f) "$verb ${(displayProgress * 100).toInt().coerceIn(0, 100)}%" else verb
        }
        txt(c, caption, l.cx, l.cancel.top - 8 * u, 15f, 500, Pal.muted, a * .9f, 1, u)
        txt(c, context.getString(R.string.cancel), l.cx, l.cancel.centerY() + 8 * u, 14.5f, 500,
            if (pressed == H_CANCEL) Pal.white else Pal.dim, a, 1, u)
    }

    private fun drawCard(c: Canvas, l: L, a: Float, scale: Float, ok: Boolean) {
        if (a <= 0f) return
        val u = l.u
        val cx = l.card.centerX(); val cy = l.card.centerY()
        val r = RectF(cx - l.card.width() * scale / 2, cy - l.card.height() * scale / 2,
            cx + l.card.width() * scale / 2, cy + l.card.height() * scale / 2)
        glow(c, cx, cy, r.width() * .95f, if (ok) Pal.purple else Pal.bad, .2f * a, unitGlow)
        pill(c, r, Pal.panel, .95f * a, 30 * u)
        stroke(c, r, Pal.white, .13f * a, u, 30 * u)
        val bx = cx; val by = r.top + 58 * u * scale
        if (ok) {
            glow(c, bx, by, 70 * u, Pal.purple, .45f * a, unitGlow)
            paint.shader = LinearGradient(bx - 30 * u, by - 30 * u, bx + 30 * u, by + 30 * u, Pal.purple, Pal.blue, Shader.TileMode.CLAMP)
            paint.alpha = (a * 255).toInt(); c.drawCircle(bx, by, 30 * u, paint); paint.shader = null; paint.alpha = 255
            line(c, bx - 11 * u, by + 1 * u, bx - 3.5f * u, by + 8.5f * u, 4.2f * u, Pal.white, a)
            line(c, bx - 3.5f * u, by + 8.5f * u, bx + 12 * u, by - 8 * u, 4.2f * u, Pal.white, a)
            txt(c, context.getString(R.string.delivered), cx, r.top + 118 * u * scale, 28f, 700, Pal.white, a, 1, u)
            txt(c, context.getString(if (incoming) R.string.files_received_title else R.string.files_sent_title),
                cx, r.top + 146 * u * scale, 14.5f, 400, Pal.muted, a, 1, u)
            val s = stats
            val values = listOf(s?.completed?.toString() ?: "0", context.readableBytes(s?.bytes ?: 0),
                context.readableTime(s?.elapsedNanos ?: 0), context.readableSpeed((s?.bytesPerSecond ?: 0.0).toLong()))
            val labels = listOf(R.string.card_files, R.string.card_transferred, R.string.card_time, R.string.card_speed)
            val gap = 10 * u; val tw = (r.width() - 44 * u - gap) / 2; val th = 68 * u * scale
            for (i in values.indices) {
                val tx = r.left + 22 * u + (i % 2) * (tw + gap)
                val ty = r.top + 172 * u * scale + (i / 2) * (th + gap)
                rect.set(tx, ty, tx + tw, ty + th)
                pill(c, rect, Pal.white, .06f * a, 18 * u)
                txt(c, values[i], tx + tw / 2, ty + th * .40f, 21f, 700, Pal.white, a, 1, u, tw - 20 * u)
                txt(c, context.getString(labels[i]), tx + tw / 2, ty + th * .77f, 12f, 400, Pal.muted, a, 1, u)
            }
        } else {
            glow(c, bx, by, 70 * u, Pal.bad, .35f * a, unitGlow)
            paint.shader = LinearGradient(bx - 30 * u, by - 30 * u, bx + 30 * u, by + 30 * u,
                lerpColor(Pal.bad, Pal.white, .1f), lerpColor(Pal.bad, Pal.purple, .6f), Shader.TileMode.CLAMP)
            paint.alpha = (a * 255).toInt(); c.drawCircle(bx, by, 30 * u, paint); paint.shader = null; paint.alpha = 255
            line(c, bx - 9 * u, by - 9 * u, bx + 9 * u, by + 9 * u, 4.2f * u, Pal.white, a)
            line(c, bx - 9 * u, by + 9 * u, bx + 9 * u, by - 9 * u, 4.2f * u, Pal.white, a)
            txt(c, context.getString(R.string.failed_title), cx, r.top + 118 * u * scale, 26f, 700, Pal.white, a, 1, u)
            text.textSize = 15 * u; text.typeface = Typeface.create(Typeface.DEFAULT, 400, false)
            val words = failText.split(' ')
            var line = ""; var row = 0
            for (word in words) {
                val cand = if (line.isEmpty()) word else "$line $word"
                if (text.measureText(cand) <= r.width() - 56 * u || line.isEmpty()) { line = cand; continue }
                txt(c, line, cx, r.top + 156 * u * scale + row * 24 * u, 15f, 400, Pal.muted, a, 1, u, r.width() - 56 * u)
                if (++row == 4) { line = ""; break }
                line = word
            }
            if (line.isNotEmpty() && row < 4) txt(c, line, cx, r.top + 156 * u * scale + row * 24 * u, 15f, 400, Pal.muted, a, 1, u, r.width() - 56 * u)
            txt(c, context.getString(R.string.files_kept), cx, r.bottom - 112 * u * scale, 13f, 400, Pal.dim, a, 1, u)
        }
        if (phase == Phase.OK || phase == Phase.FAIL || phase == Phase.DEPARTING && a > .7f) {
            primaryButton(c, l.ok, context.getString(R.string.ok), u, H_OK, a)
        }
    }

    // ================================================================ input

    private fun hit(x: Float, y: Float): Int {
        val l = layout()
        if (sheetOpen && phase == Phase.IDLE) {
            for ((i, r) in l.rows.withIndex()) if (r.contains(x, y)) return H_PHONE + i
            return if (l.sheet.contains(x, y)) H_NONE else H_SHEET_OUT
        }
        return when (phase) {
            Phase.IDLE -> when {
                uiAlpha(now()) < .5f -> H_NONE
                l.gear.contains(x, y) -> H_GEAR
                selA < .5f -> if (l.choose.contains(x, y)) H_CHOOSE else H_NONE
                l.clear.contains(x, y) -> H_CLEAR
                l.summary.contains(x, y) -> H_SUMMARY
                RectF(l.thumb).apply { inset(-10 * l.u, -10 * l.u) }.contains(x, y) -> H_THUMB
                l.target.contains(x, y) -> H_TARGET
                else -> H_NONE
            }
            Phase.LAUNCHING, Phase.FLYING -> if (now() - flightStart > 1.0 && l.cancel.contains(x, y)) H_CANCEL else H_NONE
            Phase.OK, Phase.FAIL -> if (l.ok.contains(x, y)) H_OK else H_NONE
            Phase.DEPARTING -> if (now() - departAt > 1.3 && l.ok.contains(x, y)) H_OK else H_NONE
        }
    }
    override fun onTouchEvent(e: MotionEvent): Boolean {
        val x = e.x; val y = e.y
        when (e.actionMasked) {
            MotionEvent.ACTION_DOWN -> {
                pressed = hit(x, y)
                if (pressed == H_THUMB) {
                    dragging = true; springing = false; armed = false
                    grab = x - layout().thumb.left
                    parent?.requestDisallowInterceptTouchEvent(true)
                }
                invalidate()
            }
            MotionEvent.ACTION_MOVE -> if (dragging) {
                val l = layout()
                sliderPos = ((x - grab - l.minX) / l.travel).coerceIn(0f, 1f)
                if (ready && !armed && sliderPos >= THRESHOLD) { armed = true; performHapticFeedback(HapticFeedbackConstants.CLOCK_TICK) }
                if (armed && sliderPos < THRESHOLD) armed = false
                invalidate()
            }
            MotionEvent.ACTION_UP -> {
                val was = pressed
                pressed = H_NONE
                if (dragging) releaseSlider()
                else if (was != H_NONE && hit(x, y) == was) { performClick(); click(was) }
                invalidate()
            }
            MotionEvent.ACTION_CANCEL -> {
                pressed = H_NONE
                if (dragging) { dragging = false; springing = true; springFrom = sliderPos; springAt = now() }
                invalidate()
            }
        }
        return true
    }

    override fun performClick(): Boolean { super.performClick(); return true }

    private fun releaseSlider() {
        dragging = false
        if (sliderPos >= THRESHOLD) {
            if (ready) { performHapticFeedback(HapticFeedbackConstants.CONFIRM); host?.onLaunch(); return }
            flashTarget()
        }
        springing = true; springFrom = sliderPos; springAt = now()
    }

    private fun click(id: Int) {
        val h = host ?: return
        when {
            id == H_GEAR -> h.onSettings()
            id == H_CHOOSE || id == H_SUMMARY -> h.onChoose()
            id == H_CLEAR -> h.onClear()
            id == H_TARGET -> h.onTarget()
            id == H_CANCEL -> h.onCancel()
            id == H_OK -> h.onDismiss()
            id == H_SHEET_OUT -> h.onSheetClose()
            id >= H_PHONE -> h.onSheetPick(id - H_PHONE)
        }
    }

    // ================================================================ frame rate (unchanged policy)

    // The View is not a Surface: request a high frame rate on the View (API 35+) and
    // prefer a native display mode on the window (API 34). The system can still deny it.
    private fun updateFrameRate(animate: Boolean) {
        val shouldRequest = animate && isAttachedToWindow && isShown && hasWindowFocus()
        if (shouldRequest == requestingHighRate) return
        requestingHighRate = shouldRequest
        if (Build.VERSION.SDK_INT >= 35) {
            setRequestedFrameRate(if (shouldRequest) REQUESTED_FRAME_RATE_CATEGORY_HIGH else 0f)
        }
        val activity = context as? Activity ?: return
        val params = activity.window.attributes
        if (shouldRequest) {
            previousWindowRate = params.preferredRefreshRate
            val current = display?.mode
            val modes = display?.supportedModes?.filter {
                current != null && it.physicalWidth == current.physicalWidth && it.physicalHeight == current.physicalHeight
            }.orEmpty()
            val preferred = modes.firstOrNull { abs(it.refreshRate - 120f) < 0.5f } ?: modes.maxByOrNull { it.refreshRate }
            if (preferred != null) params.preferredRefreshRate = preferred.refreshRate
        } else {
            params.preferredRefreshRate = previousWindowRate
            previousDrawNanos = 0L; diagnosticStartNanos = 0L; diagnosticIntervalNanos = 0L; diagnosticDraws = 0
        }
        activity.window.attributes = params
        if (debug) Log.d("RocketRefresh", "request=$shouldRequest preferred=${params.preferredRefreshRate}Hz " +
            "current=${display?.mode?.refreshRate}Hz (system policy controls actual mode)")
    }

    override fun onVisibilityAggregated(isVisible: Boolean) {
        super.onVisibilityAggregated(isVisible)
        updateFrameRate(isVisible && animating(now()))
        if (isVisible) invalidate()
    }

    override fun onWindowFocusChanged(hasWindowFocus: Boolean) {
        super.onWindowFocusChanged(hasWindowFocus)
        updateFrameRate(hasWindowFocus && animating(now()))
        if (hasWindowFocus) invalidate()
    }

    override fun onDetachedFromWindow() { updateFrameRate(false); super.onDetachedFromWindow() }

    private fun recordDraw(nowNanos: Long) {
        if (!debug || !requestingHighRate) return
        if (diagnosticStartNanos == 0L) diagnosticStartNanos = nowNanos
        if (previousDrawNanos != 0L) diagnosticIntervalNanos += nowNanos - previousDrawNanos
        previousDrawNanos = nowNanos
        diagnosticDraws++
        if (nowNanos - diagnosticStartNanos >= 1_000_000_000L) {
            Log.d("RocketRefresh", "mode=${display?.mode?.refreshRate}Hz, onDraw callbacks=$diagnosticDraws/" +
                "${(nowNanos - diagnosticStartNanos) / 1e9}s, mean callback spacing=" +
                "${if (diagnosticDraws > 1) diagnosticIntervalNanos / (diagnosticDraws - 1) / 1e6 else 0.0}ms (not presented-frame measurements)")
            diagnosticStartNanos = nowNanos; diagnosticDraws = 0; diagnosticIntervalNanos = 0L
        }
    }

    companion object {
        // Pixel coordinates inside res/drawable-nodpi/rocket.png (see tools/genassets): the sprite size,
        // a point inside the exhaust flame, and the unit vector along the nose.
        const val SPRITE_W = 587f
        const val SPRITE_H = 773f
        const val FLAME_X = 25f
        const val FLAME_Y = 745f
        const val AXIS_X = 0.597f
        const val AXIS_Y = -0.802f
        const val THRESHOLD = .85f
        const val H_NONE = -1
        const val H_GEAR = 0
        const val H_CHOOSE = 1
        const val H_SUMMARY = 2
        const val H_CLEAR = 3
        const val H_TARGET = 4
        const val H_THUMB = 5
        const val H_OK = 6
        const val H_CANCEL = 7
        const val H_SHEET_OUT = 8
        const val H_PHONE = 100

        fun ease(t: Double): Double { val k = t.coerceIn(0.0, 1.0); return 1 - (1 - k).pow(3) }
        fun easeInOut(t: Double): Double { val k = t.coerceIn(0.0, 1.0); return k * k * (3 - 2 * k) }
        fun approach(v: Float, target: Float, step: Float) = if (v < target) min(target, v + step) else max(target, v - step)
        fun frac(v: Float): Float = v - floor(v)
        fun frac(v: Double): Float = (v - floor(v)).toFloat()
        fun lerpColor(a: Int, b: Int, t: Float): Int {
            val k = t.coerceIn(0f, 1f)
            fun ch(shift: Int) = (((a shr shift and 255) * (1 - k) + (b shr shift and 255) * k) + .5f).toInt()
            return (0xFF shl 24) or (ch(16) shl 16) or (ch(8) shl 8) or ch(0)
        }
    }
}
