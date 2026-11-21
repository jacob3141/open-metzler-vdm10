package io.github.jacob3141.doorbell

import android.content.Context
import android.util.AttributeSet
import android.view.View
import android.widget.FrameLayout

/** Shows its child at a fixed aspect ratio, as large as fits. */
class AspectFrameLayout @JvmOverloads constructor(context: Context, attrs: AttributeSet? = null) : FrameLayout(context, attrs) {
    private var ratio = 16.0 / 9.0

    fun setAspect(width: Int, height: Int) {
        if (width <= 0 || height <= 0) return
        val r = width.toDouble() / height
        if (r != ratio) {
            ratio = r
            requestLayout()
        }
    }

    override fun onMeasure(widthMeasureSpec: Int, heightMeasureSpec: Int) {
        val maxW = MeasureSpec.getSize(widthMeasureSpec)
        val maxH = MeasureSpec.getSize(heightMeasureSpec)
        var w = maxW
        var h = (w / ratio).toInt()
        if (MeasureSpec.getMode(heightMeasureSpec) != MeasureSpec.UNSPECIFIED && h > maxH) {
            h = maxH
            w = (h * ratio).toInt()
        }
        super.onMeasure(
            View.MeasureSpec.makeMeasureSpec(w, MeasureSpec.EXACTLY),
            View.MeasureSpec.makeMeasureSpec(h, MeasureSpec.EXACTLY),
        )
    }
}
