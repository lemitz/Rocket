package dev.rocket.android

import android.app.Activity
import android.content.Context
import android.content.pm.ApplicationInfo
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.LinearGradient
import android.graphics.Paint
import android.graphics.Path
import android.graphics.Shader
import android.os.Build
import android.os.SystemClock
import android.util.Log
import android.util.TypedValue
import android.view.View
import kotlin.math.sin

/** Entirely original vector scene; no image assets or animation dependencies. */
internal class LaunchView(context: Context) : View(context) {
    private val density = resources.displayMetrics.density
    private val p = Paint(Paint.ANTI_ALIAS_FLAG)
    private var phase = 0 // 0 idle, 1 launch, 2 final flight + smoke + card
    private var launchedAt = 0L
    private var finishedAt = 0L
    private var final: TransferSnapshot? = null
    private var requestingHighRate = false
    private var previousWindowRate = 0f
    private var previousDrawNanos = 0L
    private var diagnosticStartNanos = 0L
    private var diagnosticDraws = 0
    private var diagnosticIntervalNanos = 0L
    private val debug = context.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0
    private val stars = List(46) { i ->
        Triple(((i * 73 + 19) % 101) / 101f, ((i * 47 + 13) % 97) / 97f, if (i % 5 == 0) 2f else 1f)
    }

    init { contentDescription = context.getString(R.string.rocket_ready) }

    // The View is not a Surface: request a high frame rate on the View (API 35+) and
    // prefer a native display mode on the window (API 34). The system can still deny it.
    private fun updateFrameRate(animate: Boolean) {
        val shouldRequest = animate && isAttachedToWindow && isShown && hasWindowFocus()
        if (shouldRequest == requestingHighRate) return
        requestingHighRate = shouldRequest
        if (Build.VERSION.SDK_INT >= 35) {
            setRequestedFrameRate(if (shouldRequest) View.REQUESTED_FRAME_RATE_CATEGORY_HIGH else 0f)
        }
        val activity = context as? Activity ?: return
        val params = activity.window.attributes
        if (shouldRequest) {
            previousWindowRate = params.preferredRefreshRate
            val current = display?.mode
            val modes = display?.supportedModes?.filter {
                current != null && it.physicalWidth == current.physicalWidth && it.physicalHeight == current.physicalHeight
            }.orEmpty()
            val preferred = modes.firstOrNull { kotlin.math.abs(it.refreshRate - 120f) < 0.5f }
                ?: modes.maxByOrNull { it.refreshRate }
            if (preferred != null) params.preferredRefreshRate = preferred.refreshRate
        } else {
            params.preferredRefreshRate = previousWindowRate
            previousDrawNanos = 0L
            diagnosticStartNanos = 0L
            diagnosticIntervalNanos = 0L
            diagnosticDraws = 0
        }
        activity.window.attributes = params
        if (debug) Log.d("RocketRefresh", "request=$shouldRequest preferred=${params.preferredRefreshRate}Hz " +
            "current=${display?.mode?.refreshRate}Hz (system policy controls actual mode)")
    }

    private fun animating(now: Long): Boolean = phase == 1 ||
        phase == 2 && now - finishedAt < 1_500_000_000L

    override fun onVisibilityAggregated(isVisible: Boolean) {
        super.onVisibilityAggregated(isVisible)
        val active = isVisible && animating(SystemClock.elapsedRealtimeNanos())
        updateFrameRate(active)
        if (isVisible) {
            // Repaint even if the result reveal finished while the window was hidden.
            if (active) postInvalidateOnAnimation() else invalidate()
        }
    }

    override fun onWindowFocusChanged(hasWindowFocus: Boolean) {
        super.onWindowFocusChanged(hasWindowFocus)
        updateFrameRate(hasWindowFocus && animating(SystemClock.elapsedRealtimeNanos()))
        if (hasWindowFocus && animating(SystemClock.elapsedRealtimeNanos())) postInvalidateOnAnimation()
    }

    override fun onDetachedFromWindow() {
        updateFrameRate(false)
        super.onDetachedFromWindow()
    }

    private fun recordDraw(now: Long) {
        if (!debug || !requestingHighRate) return
        if (diagnosticStartNanos == 0L) diagnosticStartNanos = now
        if (previousDrawNanos != 0L) diagnosticIntervalNanos += now - previousDrawNanos
        previousDrawNanos = now
        diagnosticDraws++
        if (now - diagnosticStartNanos >= 1_000_000_000L) {
            Log.d("RocketRefresh", "mode=${display?.mode?.refreshRate}Hz, " +
                "onDraw callbacks=$diagnosticDraws/${(now - diagnosticStartNanos) / 1e9}s, " +
                "mean callback spacing=${if (diagnosticDraws > 1) diagnosticIntervalNanos / (diagnosticDraws - 1) / 1e6 else 0.0}ms " +
                "(not presented-frame measurements)")
            diagnosticStartNanos = now
            diagnosticDraws = 0
            diagnosticIntervalNanos = 0L
        }
    }

    fun launch() {
        phase = 1
        final = null
        contentDescription = context.getString(R.string.rocket_flying)
        launchedAt = SystemClock.elapsedRealtimeNanos()
        updateFrameRate(true)
        postInvalidateOnAnimation()
    }

    fun succeed(snapshot: TransferSnapshot) {
        if (phase != 1) return
        final = snapshot
        contentDescription = context.getString(R.string.rocket_finished, snapshot.completed,
            context.readableBytes(snapshot.bytes), context.readableTime(snapshot.elapsedNanos),
            context.readableSpeed(snapshot.bytesPerSecond.toLong()))
        finishedAt = SystemClock.elapsedRealtimeNanos()
        phase = 2
        postInvalidateOnAnimation()
    }

    fun reset() {
        phase = 0
        final = null
        contentDescription = context.getString(R.string.rocket_ready)
        updateFrameRate(false)
        invalidate()
    }

    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        setMeasuredDimension(MeasureSpec.getSize(widthMeasureSpec), (300 * density).toInt())
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        val w = width.toFloat()
        val h = height.toFloat()
        val now = SystemClock.elapsedRealtimeNanos()
        val seconds = if (phase == 2) (now - finishedAt) / 1_000_000_000f else 0f
        canvas.save()
        val clip = 22 * density
        val shape = Path().apply { addRoundRect(0f, 0f, w, h, clip, clip, Path.Direction.CW) }
        canvas.clipPath(shape)
        p.shader = LinearGradient(0f, 0f, 0f, h, 0xff061532.toInt(), 0xff2079bf.toInt(), Shader.TileMode.CLAMP)
        canvas.drawRect(0f, 0f, w, h, p)
        p.shader = null
        p.color = 0x334db9ff
        canvas.drawCircle(w * .83f, h * .2f, 64 * density, p)
        p.color = 0x77bdeeff
        canvas.drawCircle(w * .83f, h * .2f, 32 * density, p)
        stars.forEach { (x, y, size) ->
            p.color = if (phase == 1 && (now / 330_000_000L + (x * 100).toLong()) % 3L == 0L) 0xff91d7ff.toInt() else 0x99ffffff.toInt()
            canvas.drawCircle(x * w, y * h * .85f, size * density, p)
        }
        p.style = Paint.Style.STROKE
        p.strokeWidth = 1.5f * density
        p.color = 0x337ad9ff
        canvas.drawOval(-w * .35f, h * .52f, w * 1.38f, h * 1.42f, p)
        p.style = Paint.Style.FILL
        // Clouds at the horizon provide depth while leaving the controls outside the scene.
        for (i in 0..7) {
            p.color = if (i % 2 == 0) 0xffb7e9fc.toInt() else 0xffe3f8ff.toInt()
            canvas.drawCircle(w * i / 7f, h + 4 * density, (25 + i % 3 * 9) * density, p)
        }
        if (phase == 2) {
            if (seconds < .85f) {
                val travel = (seconds / .85f).coerceIn(0f, 1f)
                rocket(canvas, w * .5f, h * (.45f - .85f * travel * travel), now, true)
            }
            if (seconds > .35f) smoke(canvas, w, h, ((seconds - .35f) / .75f).coerceIn(0f, 1f))
            if (seconds > .9f) final?.let { card(canvas, w, h, it, ((seconds - .9f) / .5f).coerceIn(0f, 1f)) }
        } else {
            val lift = if (phase == 1) ((now - launchedAt) / 1_200_000_000f).coerceIn(0f, 1f) else 0f
            val bob = if (phase == 1) sin(now / 250_000_000.0).toFloat() * 3 * density else 0f
            rocket(canvas, w * .5f, h * (.72f - .27f * lift) + bob, now, phase == 1)
        }
        canvas.restore()
        val active = animating(now)
        updateFrameRate(active)
        recordDraw(now)
        if (active && requestingHighRate) postInvalidateOnAnimation()
    }

    private fun rocket(canvas: Canvas, x: Float, y: Float, now: Long, flying: Boolean) {
        canvas.save()
        canvas.translate(x, y)
        canvas.scale(density, density)
        if (flying) {
            val flicker = 9f * sin(now / 95_000_000.0).toFloat()
            p.color = 0x889bdeff.toInt()
            canvas.drawOval(-20f, 26f, 20f, 100f + flicker, p)
            p.color = 0xffffc46b.toInt()
            canvas.drawOval(-11f, 34f, 11f, 78f + flicker, p)
            p.color = Color.WHITE
            canvas.drawOval(-5f, 36f, 5f, 59f, p)
        }
        p.color = 0xff2763ad.toInt()
        path(canvas, -17f, 23f, -34f, 46f, -31f, 12f)
        path(canvas, 17f, 23f, 34f, 46f, 31f, 12f)
        p.color = 0xffeefaff.toInt()
        val body = Path().apply {
            moveTo(0f, -65f); cubicTo(30f, -47f, 24f, -6f, 18f, 33f)
            quadTo(0f, 42f, -18f, 33f); cubicTo(-24f, -6f, -30f, -47f, 0f, -65f); close()
        }
        canvas.drawPath(body, p)
        p.color = 0xff50b1e7.toInt()
        val nose = Path().apply { moveTo(0f, -65f); quadTo(17f, -54f, 21f, -34f); quadTo(0f, -42f, -21f, -34f); quadTo(-17f, -54f, 0f, -65f); close() }
        canvas.drawPath(nose, p)
        p.color = 0xff194773.toInt()
        canvas.drawCircle(0f, -17f, 13f, p)
        p.color = 0xff82dafa.toInt()
        canvas.drawCircle(0f, -17f, 9f, p)
        p.color = 0xaaffffff.toInt()
        canvas.drawCircle(-3f, -21f, 3f, p)
        p.color = 0xff2872ac.toInt()
        canvas.drawRoundRect(-9f, 31f, 9f, 40f, 3f, 3f, p)
        canvas.restore()
    }

    private fun path(canvas: Canvas, x1: Float, y1: Float, x2: Float, y2: Float, x3: Float, y3: Float) {
        canvas.drawPath(Path().apply { moveTo(x1, y1); lineTo(x2, y2); lineTo(x3, y3); close() }, p)
    }

    private fun smoke(canvas: Canvas, w: Float, h: Float, progress: Float) {
        val spread = progress * w * .36f
        for (i in 0..11) {
            val t = i / 11f
            val x = w * .5f + (t - .5f) * spread * 2
            val y = h * (.58f + .13f * sin(i * 2.7).toFloat())
            p.color = if (i % 2 == 0) 0xffd7f5ff.toInt() else 0xff91d7f4.toInt()
            p.alpha = (165 * (1f - progress * .25f)).toInt()
            canvas.drawCircle(x, y, (17 + i % 4 * 8) * density * progress, p)
        }
        p.alpha = 255
    }

    private fun card(canvas: Canvas, w: Float, h: Float, snapshot: TransferSnapshot, alpha: Float) {
        val left = 16 * density
        val right = w - left
        p.color = Color.WHITE
        p.alpha = (242 * alpha).toInt()
        canvas.drawRoundRect(left, 22 * density, right, h - 18 * density, 20 * density, 20 * density, p)
        p.alpha = 255
        text(canvas, context.getString(R.string.flight_complete), left + 18 * density, 60 * density, 16f,
            0xff164678.toInt(), alpha, true, right - left - 36 * density)
        val rows = listOf(
            context.getString(R.string.card_files) to snapshot.completed.toString(),
            context.getString(R.string.card_transferred) to context.readableBytes(snapshot.bytes),
            context.getString(R.string.card_time) to context.readableTime(snapshot.elapsedNanos),
            context.getString(R.string.card_speed) to context.readableSpeed(snapshot.bytesPerSecond.toLong())
        )
        rows.forEachIndexed { i, (label, value) ->
            val y = (106 + i * 42) * density
            val available = right - left - 36 * density
            text(canvas, label, left + 18 * density, y, 13f, 0xff51738e.toInt(), alpha, false,
                available * .47f)
            p.textAlign = Paint.Align.RIGHT
            text(canvas, value, right - 18 * density, y, 14f, 0xff103b6d.toInt(), alpha, true,
                available * .51f)
            p.textAlign = Paint.Align.LEFT
        }
    }

    private fun text(canvas: Canvas, value: String, x: Float, y: Float, sp: Float, color: Int, alpha: Float,
                     bold: Boolean, maxWidth: Float) {
        p.color = color
        p.alpha = (255 * alpha).toInt()
        p.textSize = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_SP, sp, resources.displayMetrics)
        p.typeface = if (bold) android.graphics.Typeface.create("sans-serif-medium", 0) else android.graphics.Typeface.DEFAULT
        if (p.measureText(value) > maxWidth) p.textSize *= maxWidth / p.measureText(value)
        canvas.drawText(value, x, y, p)
        p.alpha = 255
    }
}
