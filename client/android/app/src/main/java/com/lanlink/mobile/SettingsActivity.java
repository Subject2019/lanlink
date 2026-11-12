package com.lanlink.mobile;

import android.Manifest;
import android.app.Activity;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.provider.Settings;
import android.text.InputType;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

/** 原生设置页：服务器、设备名、登录态、下载目录、通知权限都在这里管理。 */
public class SettingsActivity extends Activity {
    static final String PREF = "lanlink";
    static final String KEY_SERVER = "server";
    static final String KEY_TOKEN = "token";
    static final String KEY_NAME = "device_name";
    static final String KEY_DOWNLOAD_TREE = "download_tree";

    private static final int REQ_TREE = 2001;
    private static final int REQ_NOTIFICATION = 2002;
    private static final int FLAGS = Intent.FLAG_GRANT_READ_URI_PERMISSION
            | Intent.FLAG_GRANT_WRITE_URI_PERMISSION
            | Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION;

    private SharedPreferences prefs;
    private TextView permView;
    private TextView dirView;
    private TextView serverView;
    private TextView tokenView;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        prefs = getSharedPreferences(PREF, Context.MODE_PRIVATE);
        setTitle("LanLink 设置");

        ScrollView scroll = new ScrollView(this);
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        int pad = dp(20);
        box.setPadding(pad, pad, pad, pad);
        box.setBackgroundColor(Color.parseColor("#0d1117"));
        scroll.addView(box, new ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));

        box.addView(section("服务器"));
        serverView = info(box, prefs.getString(KEY_SERVER, "（未设置）"));
        EditText input = new EditText(this);
        input.setInputType(InputType.TYPE_TEXT_VARIATION_URI);
        input.setHint("http://192.168.1.10:8787");
        input.setTextColor(Color.parseColor("#e6edf3"));
        input.setText(prefs.getString(KEY_SERVER, ""));
        box.addView(input);
        box.addView(button("保存地址（会重新加入）", v -> {
            String s = input.getText().toString().trim();
            if (s.isEmpty()) return;
            if (!s.startsWith("http://") && !s.startsWith("https://")) s = "http://" + s;
            while (s.endsWith("/")) s = s.substring(0, s.length() - 1);
            prefs.edit().putString(KEY_SERVER, s).remove(KEY_TOKEN).apply();
            serverView.setText(s);
            refreshToken();
            toast("已保存，返回后会要求重新加入");
        }));
        box.addView(button("重新自动发现 NAS", v -> new Thread(() -> {
            String found = MainActivity.discover();
            runOnUiThread(() -> {
                if (found == null) {
                    toast("没找到 NAS，请手动填地址");
                } else {
                    prefs.edit().putString(KEY_SERVER, found).remove(KEY_TOKEN).apply();
                    input.setText(found);
                    serverView.setText(found);
                    refreshToken();
                    toast("已找到：" + found);
                }
            });
        }).start()));
        box.addView(button("分享服务地址给其它人", v ->
                Sharer.shareServer(this, prefs.getString(KEY_SERVER, ""))));

        box.addView(section("本机设备名"));
        EditText nameInput = new EditText(this);
        nameInput.setInputType(InputType.TYPE_CLASS_TEXT);
        nameInput.setTextColor(Color.parseColor("#e6edf3"));
        nameInput.setText(prefs.getString(KEY_NAME, ""));
        nameInput.setHint("我的手机");
        box.addView(nameInput);
        box.addView(button("改名", v -> {
            String name = nameInput.getText().toString().trim();
            String server = prefs.getString(KEY_SERVER, "");
            String token = prefs.getString(KEY_TOKEN, "");
            if (name.isEmpty()) {
                toast("名称不能为空");
                return;
            }
            if (server.isEmpty() || token.isEmpty()) {
                prefs.edit().putString(KEY_NAME, name).apply();
                toast("已记住，加入后生效");
                return;
            }
            new Thread(() -> {
                boolean ok;
                try {
                    Api.rename(server, token, name);
                    ok = true;
                } catch (Exception e) {
                    ok = false;
                }
                final boolean done = ok;
                runOnUiThread(() -> {
                    if (done) {
                        prefs.edit().putString(KEY_NAME, name).apply();
                        toast("设备名已更新");
                    } else {
                        toast("改名失败，请检查网络或重新加入");
                    }
                });
            }).start();
        }));

        box.addView(section("登录状态"));
        tokenView = info(box, "");
        refreshToken();
        box.addView(button("清除登录状态（重新加入）", v -> {
            prefs.edit().remove(KEY_TOKEN).apply();
            refreshToken();
            toast("已清除，返回后重新加入");
        }));

        box.addView(section("访问权限"));
        permView = info(box, "");
        refreshPerm();
        box.addView(button("授予通知权限", v -> requestNotify()));
        box.addView(button("打开系统权限设置", v -> {
            Intent i = new Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS);
            i.setData(Uri.parse("package:" + getPackageName()));
            startActivity(i);
        }));

        box.addView(section("文件访问目录"));
        box.addView(tip("下载默认写入系统「下载」目录。也可以授权一个目录，"
                + "让 LanLink 直接写进去（长期有效，卸载应用前都记住）。"));
        dirView = info(box, "");
        refreshDir();
        box.addView(button("选择目录", v -> {
            Intent i = new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE);
            i.addFlags(FLAGS);
            try {
                startActivityForResult(i, REQ_TREE);
            } catch (Exception e) {
                toast("系统不支持目录选择");
            }
        }));
        box.addView(button("恢复默认（系统下载目录）", v -> {
            String old = prefs.getString(KEY_DOWNLOAD_TREE, null);
            if (old != null) {
                try {
                    getContentResolver().releasePersistableUriPermission(Uri.parse(old),
                            Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_GRANT_WRITE_URI_PERMISSION);
                } catch (Exception ignored) {
                }
            }
            prefs.edit().remove(KEY_DOWNLOAD_TREE).apply();
            refreshDir();
            toast("已恢复默认");
        }));

        box.addView(section("其它"));
        box.addView(button("返回", v -> finish()));

        setContentView(scroll);
    }

    @Override
    protected void onResume() {
        super.onResume();
        refreshPerm();
    }

    private void refreshToken() {
        String token = prefs.getString(KEY_TOKEN, "");
        boolean ok = !token.isEmpty();
        tokenView.setText(ok ? "已加入 ✓（令牌由 App 自己保存，不依赖浏览器 Cookie）" : "未加入，返回后会显示加入页");
        tokenView.setTextColor(Color.parseColor(ok ? "#3fb950" : "#f0883e"));
    }

    private void refreshPerm() {
        boolean ok = Build.VERSION.SDK_INT < 33
                || checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED;
        permView.setText("通知权限：" + (ok ? "已授予 ✓" : "未授予（下载完成提醒不可用）"));
        permView.setTextColor(Color.parseColor(ok ? "#3fb950" : "#f0883e"));
    }

    private void refreshDir() {
        String uri = prefs.getString(KEY_DOWNLOAD_TREE, null);
        dirView.setText(uri == null ? "当前：系统「下载」目录" : "当前：已授权目录\n" + uri);
    }

    private void requestNotify() {
        if (Build.VERSION.SDK_INT >= 33) {
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, REQ_NOTIFICATION);
        } else {
            toast("当前系统无需动态申请");
        }
    }

    @Override
    public void onRequestPermissionsResult(int code, String[] permissions, int[] results) {
        super.onRequestPermissionsResult(code, permissions, results);
        if (code == REQ_NOTIFICATION) refreshPerm();
    }

    @Override
    protected void onActivityResult(int code, int result, Intent data) {
        super.onActivityResult(code, result, data);
        if (code == REQ_TREE && result == RESULT_OK && data != null && data.getData() != null) {
            Uri uri = data.getData();
            try {
                getContentResolver().takePersistableUriPermission(uri,
                        Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_GRANT_WRITE_URI_PERMISSION);
                prefs.edit().putString(KEY_DOWNLOAD_TREE, uri.toString()).apply();
                refreshDir();
                toast("目录已授权");
            } catch (Exception e) {
                toast("授权失败，请换一个目录");
            }
        }
    }

    private TextView section(String text) {
        TextView t = new TextView(this);
        t.setText(text);
        t.setTextSize(17);
        t.setTextColor(Color.parseColor("#58a6ff"));
        t.setPadding(0, dp(22), 0, dp(6));
        return t;
    }

    private TextView info(LinearLayout parent, String text) {
        TextView t = new TextView(this);
        t.setText(text);
        t.setTextSize(14);
        t.setTextColor(Color.parseColor("#e6edf3"));
        t.setPadding(0, dp(4), 0, dp(8));
        parent.addView(t);
        return t;
    }

    private TextView tip(String text) {
        TextView t = new TextView(this);
        t.setText(text);
        t.setTextSize(12);
        t.setTextColor(Color.parseColor("#8b949e"));
        return t;
    }

    private Button button(String text, View.OnClickListener l) {
        Button b = new Button(this);
        b.setText(text);
        b.setAllCaps(false);
        b.setGravity(Gravity.START | Gravity.CENTER_VERTICAL);
        b.setOnClickListener(l);
        return b;
    }

    private int dp(int v) {
        return (int) (v * getResources().getDisplayMetrics().density);
    }

    private void toast(String s) {
        Toast.makeText(this, s, Toast.LENGTH_SHORT).show();
    }
}
