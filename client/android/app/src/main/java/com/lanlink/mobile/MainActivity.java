package com.lanlink.mobile;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.app.DownloadManager;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.graphics.Color;
import android.net.Uri;
import android.os.Bundle;
import android.os.Environment;
import android.text.InputType;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.CookieManager;
import android.webkit.DownloadListener;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.TextView;
import android.widget.Toast;

import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.InetAddress;
import java.util.ArrayList;
import java.util.List;

/**
 * LanLink 安卓客户端：只是一个极小的 WebView 壳，界面完全由 NAS 服务端下发。
 * 启动时先尝试 UDP 广播自动发现 NAS，找不到再让用户手填地址。
 */
public class MainActivity extends Activity {
    private static final int DISCOVERY_PORT = 8788;
    private static final String DISCOVERY_MAGIC = "lanlink.discover";
    private static final int REQ_FILE = 1001;

    private WebView webView;
    private SharedPreferences prefs;
    private android.webkit.ValueCallback<Uri[]> fileCallback;

    @SuppressLint("SetJavaScriptEnabled")
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        prefs = getSharedPreferences("lanlink", Context.MODE_PRIVATE);
        webView = buildWebView();
        String saved = prefs.getString("server", null);
        if (saved == null || saved.isEmpty()) {
            showSetup("");
            discoverAsync();
        } else {
            load(saved);
        }
    }

    private WebView buildWebView() {
        WebView v = new WebView(this);
        v.setLayoutParams(new ViewGroup.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        v.setBackgroundColor(Color.parseColor("#0d1117"));

        WebSettings s = v.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setDatabaseEnabled(true);
        s.setLoadWithOverviewMode(true);
        s.setUseWideViewPort(true);
        s.setMediaPlaybackRequiresUserGesture(false);
        s.setMixedContentMode(WebSettings.MIXED_CONTENT_ALWAYS_ALLOW);
        s.setUserAgentString(s.getUserAgentString() + " LanLinkApp/1.0");

        CookieManager.getInstance().setAcceptCookie(true);
        CookieManager.getInstance().setAcceptThirdPartyCookies(v, true);

        v.setWebViewClient(new WebViewClient() {
            @Override
            public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                if (request.isForMainFrame()) {
                    Toast.makeText(MainActivity.this, "连不上 NAS，请检查地址", Toast.LENGTH_LONG).show();
                }
            }
        });

        v.setWebChromeClient(new WebChromeClient() {
            @Override
            public boolean onShowFileChooser(WebView view,
                                             android.webkit.ValueCallback<Uri[]> callback,
                                             FileChooserParams params) {
                fileCallback = callback;
                Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT);
                intent.addCategory(Intent.CATEGORY_OPENABLE);
                intent.setType("*/*");
                intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
                try {
                    startActivityForResult(intent, REQ_FILE);
                } catch (Exception e) {
                    fileCallback = null;
                    return false;
                }
                return true;
            }
        });

        v.setDownloadListener(new DownloadListener() {
            @Override
            public void onDownloadStart(String url, String userAgent, String contentDisposition,
                                        String mimetype, long contentLength) {
                startDownload(url);
            }
        });
        return v;
    }

    private void startDownload(String url) {
        try {
            String name = Uri.parse(url).getQueryParameter("n");
            if (name == null || name.trim().isEmpty()) {
                name = "lanlink-" + System.currentTimeMillis();
            }
            DownloadManager.Request req = new DownloadManager.Request(Uri.parse(url));
            String cookie = CookieManager.getInstance().getCookie(url);
            if (cookie != null) {
                req.addRequestHeader("Cookie", cookie);
            }
            req.setTitle(name);
            req.setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED);
            req.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name);
            DownloadManager dm = (DownloadManager) getSystemService(Context.DOWNLOAD_SERVICE);
            if (dm != null) {
                dm.enqueue(req);
                Toast.makeText(this, "开始下载：" + name, Toast.LENGTH_SHORT).show();
            }
        } catch (Exception e) {
            Toast.makeText(this, "下载失败：" + e.getMessage(), Toast.LENGTH_LONG).show();
        }
    }

    private void load(String base) {
        setContentView(webView);
        String url = base.endsWith("/") ? base : base + "/";
        webView.loadUrl(url);
    }

    /** 后台线程广播探测，找到后直接进入并记住地址。 */
    private void discoverAsync() {
        new Thread(() -> {
            String found = discover();
            runOnUiThread(() -> {
                if (found != null) {
                    prefs.edit().putString("server", found).apply();
                    load(found);
                }
            });
        }).start();
    }

    private String discover() {
        DatagramSocket socket = null;
        try {
            socket = new DatagramSocket();
            socket.setBroadcast(true);
            socket.setSoTimeout(900);
            byte[] payload = DISCOVERY_MAGIC.getBytes("UTF-8");
            for (int attempt = 0; attempt < 3; attempt++) {
                socket.send(new DatagramPacket(payload, payload.length,
                        InetAddress.getByName("255.255.255.255"), DISCOVERY_PORT));
                long deadline = System.currentTimeMillis() + 900;
                while (System.currentTimeMillis() < deadline) {
                    byte[] buf = new byte[256];
                    DatagramPacket resp = new DatagramPacket(buf, buf.length);
                    socket.receive(resp);
                    String body = new String(resp.getData(), 0, resp.getLength(), "UTF-8");
                    int idx = body.indexOf("\"port\":");
                    if (idx >= 0) {
                        String rest = body.substring(idx + 7).trim();
                        StringBuilder digits = new StringBuilder();
                        for (char c : rest.toCharArray()) {
                            if (Character.isDigit(c)) digits.append(c);
                            else break;
                        }
                        if (digits.length() > 0) {
                            String ip = resp.getAddress().getHostAddress();
                            return "http://" + ip + ":" + digits;
                        }
                    }
                }
            }
        } catch (Exception ignored) {
        } finally {
            if (socket != null) socket.close();
        }
        return null;
    }

    /** 首次使用或连不上时的地址填写界面。 */
    private void showSetup(String preset) {
        LinearLayout box = new LinearLayout(this);
        box.setOrientation(LinearLayout.VERTICAL);
        box.setGravity(Gravity.CENTER_VERTICAL);
        int pad = (int) (24 * getResources().getDisplayMetrics().density);
        box.setPadding(pad, pad, pad, pad);
        box.setBackgroundColor(Color.parseColor("#0d1117"));

        TextView title = new TextView(this);
        title.setText("连接 LanLink");
        title.setTextSize(24);
        title.setTextColor(Color.parseColor("#e6edf3"));
        box.addView(title);

        TextView hint = new TextView(this);
        hint.setText("自动发现没找到 NAS。请输入 NAS 上 LanLink 服务端的地址（启动时会打印，形如 http://192.168.1.10:8787）。");
        hint.setTextSize(13);
        hint.setTextColor(Color.parseColor("#8b949e"));
        hint.setPadding(0, pad / 2, 0, pad / 2);
        box.addView(hint);

        EditText input = new EditText(this);
        input.setInputType(InputType.TYPE_TEXT_VARIATION_URI);
        input.setHint("http://192.168.1.10:8787");
        input.setTextColor(Color.parseColor("#e6edf3"));
        input.setText(preset);
        box.addView(input);

        Button btn = new Button(this);
        btn.setText("连接");
        btn.setOnClickListener(v -> {
            String url = input.getText().toString().trim();
            if (url.isEmpty()) return;
            if (!url.startsWith("http://") && !url.startsWith("https://")) {
                url = "http://" + url;
            }
            while (url.endsWith("/")) url = url.substring(0, url.length() - 1);
            prefs.edit().putString("server", url).apply();
            load(url);
        });
        box.addView(btn);

        Button retry = new Button(this);
        retry.setText("重新自动发现");
        retry.setOnClickListener(v -> {
            showSetup(input.getText().toString().trim());
            discoverAsync();
        });
        box.addView(retry);

        setContentView(box);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        if (requestCode == REQ_FILE) {
            if (fileCallback == null) return;
            List<Uri> uris = new ArrayList<>();
            if (resultCode == RESULT_OK && data != null) {
                if (data.getClipData() != null) {
                    for (int i = 0; i < data.getClipData().getItemCount(); i++) {
                        uris.add(data.getClipData().getItemAt(i).getUri());
                    }
                } else if (data.getData() != null) {
                    uris.add(data.getData());
                }
            }
            fileCallback.onReceiveValue(uris.isEmpty() ? null : uris.toArray(new Uri[0]));
            fileCallback = null;
            return;
        }
        super.onActivityResult(requestCode, resultCode, data);
    }

    @Override
    public void onBackPressed() {
        if (webView != null && webView.canGoBack() && webView.getParent() != null) {
            webView.goBack();
        } else {
            super.onBackPressed();
        }
    }

    @Override
    protected void onDestroy() {
        if (webView != null) {
            webView.destroy();
        }
        super.onDestroy();
    }
}
