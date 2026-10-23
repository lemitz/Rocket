package dev.rocket.android

import android.app.Activity
import android.content.res.ColorStateList
import android.graphics.Color
import android.graphics.Typeface
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.RippleDrawable
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView

/**
 * Everything that is not "pick files, slide, fly": which PC, where files land, a connection
 * check. Plain platform views in the same dark style; the main screen never shows any of it.
 */
internal class SettingsPanel(private val activity: Activity, private val host: Host) {
    interface Host {
        fun pcSummary(): String
        fun folderSummary(): String
        fun deviceSummary(): String
        fun onBack()
        fun onFindPc()
        fun onEnterIp()
        fun onChooseFolder()
        fun onUseDownloads()
        fun onCheck()
    }

    private val d = activity.resources.displayMetrics.density
    private fun dp(v: Int) = (v * d).toInt()
    private lateinit var pcValue: TextView
    private lateinit var folderValue: TextView
    private lateinit var deviceValue: TextView
    private lateinit var checkResult: TextView

    val view: View = build()

    private fun build(): View {
        val root = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
            setBackgroundColor(Pal.bg0)
            isClickable = true // swallow touches meant for the scene underneath
            setOnApplyWindowInsetsListener { v, insets ->
                val bars = insets.getInsets(WindowInsets.Type.systemBars())
                v.setPadding(bars.left, bars.top, bars.right, bars.bottom)
                insets
            }
        }
        val header = LinearLayout(activity).apply {
            orientation = LinearLayout.HORIZONTAL
            gravity = Gravity.CENTER_VERTICAL
            setPadding(dp(8), dp(8), dp(16), dp(8))
        }
        header.addView(TextView(activity).apply {
            text = "‹"
            textSize = 34f
            setTextColor(Pal.white)
            gravity = Gravity.CENTER
            contentDescription = activity.getString(R.string.back)
            background = ripple(Color.TRANSPARENT, 0, dp(24))
            setOnClickListener { host.onBack() }
        }, LinearLayout.LayoutParams(dp(48), dp(48)))
        header.addView(TextView(activity).apply {
            text = activity.getString(R.string.settings)
            textSize = 22f
            setTextColor(Pal.white)
            typeface = Typeface.create(Typeface.DEFAULT, 700, false)
            setPadding(dp(6), 0, 0, 0)
        })
        root.addView(header)

        val body = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(16), dp(4), dp(16), dp(28))
        }
        root.addView(ScrollView(activity).apply { addView(body) },
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))

        pcValue = value(); folderValue = value(); deviceValue = value()
        checkResult = TextView(activity).apply {
            textSize = 14f; setTextColor(Pal.muted); setLineSpacing(0f, 1.15f)
            setPadding(0, dp(12), 0, 0); visibility = View.GONE
        }
        body.addView(card(R.string.section_pc, pcValue,
            buttons(R.string.find_pc to { host.onFindPc() }, R.string.enter_ipv4_manually to { host.onEnterIp() })))
        body.addView(card(R.string.section_folder, folderValue,
            buttons(R.string.choose_folder to { host.onChooseFolder() }, R.string.use_downloads to { host.onUseDownloads() })))
        body.addView(card(R.string.section_check,
            buttons(R.string.check_now to { host.onCheck() }), checkResult))
        body.addView(card(R.string.section_phone, deviceValue))
        body.addView(TextView(activity).apply {
            text = activity.getString(R.string.network_warning)
            textSize = 12.5f; setTextColor(Pal.dim); setPadding(dp(6), dp(6), dp(6), 0)
        })
        refresh()
        return root
    }

    fun refresh() {
        pcValue.text = host.pcSummary()
        folderValue.text = host.folderSummary()
        deviceValue.text = host.deviceSummary()
    }

    fun showCheck(lines: List<Pair<Boolean, String>>) {
        checkResult.visibility = View.VISIBLE
        val text = android.text.SpannableStringBuilder()
        for ((ok, line) in lines) {
            val start = text.length
            text.append(if (ok) "●  " else "●  ")
            text.setSpan(android.text.style.ForegroundColorSpan(if (ok) Pal.good else Pal.warn), start, start + 1,
                android.text.Spanned.SPAN_EXCLUSIVE_EXCLUSIVE)
            text.append(line).append('\n')
        }
        checkResult.text = text.trimEnd()
    }

    fun clearCheck() { checkResult.visibility = View.GONE; checkResult.text = "" }

    // ---- small style helpers

    private fun value() = TextView(activity).apply {
        textSize = 16f; setTextColor(Pal.white)
        typeface = Typeface.create(Typeface.DEFAULT, 500, false)
        setPadding(0, 0, 0, dp(2))
    }

    private fun card(title: Int, vararg content: View): View {
        val box = LinearLayout(activity).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(18), dp(16), dp(18), dp(18))
            background = GradientDrawable().apply {
                cornerRadius = dp(22).toFloat()
                setColor(0x0FFFFFFF)
                setStroke(dp(1), 0x1AFFFFFF)
            }
        }
        box.addView(TextView(activity).apply {
            text = activity.getString(title).uppercase()
            textSize = 11.5f; letterSpacing = .08f; setTextColor(Pal.purple)
            typeface = Typeface.create(Typeface.DEFAULT, 600, false)
            setPadding(0, 0, 0, dp(10))
        })
        content.forEach { box.addView(it) }
        return box.also {
            it.layoutParams = LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)
                .apply { bottomMargin = dp(14) }
        }
    }

    private fun buttons(vararg items: Pair<Int, () -> Unit>): View {
        val row = LinearLayout(activity).apply { orientation = LinearLayout.HORIZONTAL; setPadding(0, dp(10), 0, 0) }
        items.forEachIndexed { i, (label, action) ->
            row.addView(TextView(activity).apply {
                text = activity.getString(label)
                textSize = 15f; setTextColor(Pal.white); gravity = Gravity.CENTER
                typeface = Typeface.create(Typeface.DEFAULT, 600, false)
                background = ripple(0x14FFFFFF, 0x1FFFFFFF, dp(19))
                isClickable = true
                setOnClickListener { action() }
            }, LinearLayout.LayoutParams(0, dp(42), 1f).apply { if (i > 0) marginStart = dp(10) })
        }
        return row
    }

    private fun ripple(fill: Int, stroke: Int, radius: Int): RippleDrawable {
        val shape = GradientDrawable().apply {
            cornerRadius = radius.toFloat(); setColor(fill)
            if (stroke != 0) setStroke(dp(1), stroke)
        }
        return RippleDrawable(ColorStateList.valueOf(0x33FFFFFF), shape, null)
    }
}
