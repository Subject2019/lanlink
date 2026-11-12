package com.lanlink.mobile;

import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Paint;
import android.graphics.RectF;
import android.util.AttributeSet;
import android.view.View;

/** 文件卡片旁边的环形进度条：0-100 显示百分比，负数表示拿不到总大小。 */
public class RingProgress extends View {

    private final Paint back = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint front = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final Paint label = new Paint(Paint.ANTI_ALIAS_FLAG);
    private final RectF arc = new RectF();
    private int percent = 0;
    private boolean unknown = false;

    public RingProgress(Context context, AttributeSet attrs) {
        super(context, attrs);
        float width = dp(3);
        back.setStyle(Paint.Style.STROKE);
        back.setStrokeWidth(width);
        back.setColor(0x33ffffff);
        front.setStyle(Paint.Style.STROKE);
        front.setStrokeWidth(width);
        front.setStrokeCap(Paint.Cap.ROUND);
        front.setColor(Color.parseColor("#2f81f7"));
        label.setColor(Color.parseColor("#e6edf3"));
        label.setTextSize(dp(10));
        label.setTextAlign(Paint.Align.CENTER);
    }

    public void setValue(int value) {
        unknown = value < 0;
        percent = unknown ? 0 : Math.max(0, Math.min(100, value));
        invalidate();
    }

    @Override
    protected void onDraw(Canvas canvas) {
        super.onDraw(canvas);
        float w = getWidth();
        float h = getHeight();
        float r = Math.min(w, h) / 2f - dp(4);
        float cx = w / 2f;
        float cy = h / 2f;
        arc.set(cx - r, cy - r, cx + r, cy + r);
        canvas.drawArc(arc, 0, 360, false, back);
        canvas.drawArc(arc, -90, unknown ? 300 : 360f * percent / 100f, false, front);
        String text = unknown ? "…" : percent + "%";
        Paint.FontMetrics fm = label.getFontMetrics();
        canvas.drawText(text, cx, cy - (fm.ascent + fm.descent) / 2f, label);
    }

    private float dp(float v) {
        return v * getResources().getDisplayMetrics().density;
    }
}
