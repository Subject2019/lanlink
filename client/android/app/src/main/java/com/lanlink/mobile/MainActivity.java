package com.lanlink.mobile;

import android.Manifest;
import android.app.Activity;
import android.app.AlertDialog;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.content.ClipData;
import android.content.ClipboardManager;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.inputmethod.EditorInfo;
import android.widget.BaseAdapter;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ListView;
import android.widget.ProgressBar;
import android.widget.TextView;
import android.widget.Toast;

import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.InetAddress;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Date;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;

/**
 * LanLink 安卓客户端：纯原生界面，直连服务端 REST/SSE 接口。
 * 不加载任何网页，也不依赖 WebView / CookieManager / JS bridge。
 */
public class MainActivity extends Activity {
    private static final int DISCOVERY_PORT = 8788;
    private static final String DISCOVERY_MAGIC = "lanlink.discover";
    private static final int REQ_FILES = 1001;
    private static final int REQ_NOTIFICATION = 1003;
    private static final String CHANNEL = "lanlink";
    private static final String EXTRA_HANDLED = "lanlink.share.handled";

    private SharedPreferences prefs;
    private final Handler ui = new Handler(Looper.getMainLooper());
    private final List<Api.Msg> messages = new ArrayList<>();
    private final Map<String, Api.Msg> localMsgs = new LinkedHashMap<>();
    private final Map<String, Integer> progress = new HashMap<>();
    private MessageAdapter adapter;

    private View mainPanel, joinPanel, devicesStrip, transferWrap;
    private ListView listView;
    private TextView subtitle, transferText, joinError, versionText;
    private LinearLayout devicesBox, codeRow;
    private EditText input, serverInput, nameInput, codeInput;
    private ProgressBar transferBar;
    private Button btnJoin, btnDiscover;

    private String server = "", token = "", myId = "";
    private volatile boolean running;
    private boolean refreshing;
    private boolean joining;
    private Api.EventStream eventStream;
    private Runnable pollTask;
    private SharePayload pendingShare;

    private static class SharePayload {
        final String text;
        final List<Uri> uris;

        SharePayload(String text, List<Uri> uris) {
            this.text = text;
            this.uris = uris == null ? new ArrayList<>() : uris;
        }

        boolean isEmpty() {
            return (text == null || text.isEmpty()) && uris.isEmpty();
        }
    }

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        prefs = getSharedPreferences(SettingsActivity.PREF, Context.MODE_PRIVATE);
        setContentView(R.layout.activity_main);
        bindViews();

        adapter = new MessageAdapter();
        listView.setAdapter(adapter);
        wire();

        running = true;
        versionText.setText("LanLink 原生客户端 v" + appVersion() + " · 无需浏览器内核");
        server = prefs.getString(SettingsActivity.KEY_SERVER, "");
        token = prefs.getString(SettingsActivity.KEY_TOKEN, "");

        askNotificationPermission();

        if (server.isEmpty() || token.isEmpty()) {
            showJoin();
            if (server.isEmpty()) discoverAsync(false);
        } else {
            verifyAndEnter();
        }
        handleShareIntent(getIntent());
    }

    private void bindViews() {
        mainPanel = findViewById(R.id.mainPanel);
        joinPanel = findViewById(R.id.joinPanel);
        devicesStrip = findViewById(R.id.devicesStrip);
        transferWrap = findViewById(R.id.transferWrap);
        listView = findViewById(R.id.list);
        subtitle = findViewById(R.id.subtitle);
        transferText = findViewById(R.id.transferText);
        transferBar = findViewById(R.id.transferBar);
        joinError = findViewById(R.id.joinError);
        versionText = findViewById(R.id.versionText);
        devicesBox = findViewById(R.id.devicesBox);
        codeRow = findViewById(R.id.codeRow);
        input = findViewById(R.id.input);
        serverInput = findViewById(R.id.serverInput);
        nameInput = findViewById(R.id.nameInput);
        codeInput = findViewById(R.id.codeInput);
        btnJoin = findViewById(R.id.btnJoin);
        btnDiscover = findViewById(R.id.btnDiscover);
    }

    private void wire() {
        findViewById(R.id.btnSettings).setOnClickListener(v ->
                startActivity(new Intent(this, SettingsActivity.class)));
        findViewById(R.id.btnJoinSettings).setOnClickListener(v ->
                startActivity(new Intent(this, SettingsActivity.class)));
        findViewById(R.id.btnDevices).setOnClickListener(v -> {
            boolean show = devicesStrip.getVisibility() != View.VISIBLE;
            devicesStrip.setVisibility(show ? View.VISIBLE : View.GONE);
            if (show) refresh();
        });
        findViewById(R.id.btnAttach).setOnClickListener(v -> launchFilePicker());
        findViewById(R.id.btnSend).setOnClickListener(v -> sendText());
        input.setOnEditorActionListener((v, actionId, event) -> {
            if (actionId == EditorInfo.IME_ACTION_SEND) {
                sendText();
                return true;
            }
            return false;
        });
        btnJoin.setOnClickListener(v -> doJoin());
        btnDiscover.setOnClickListener(v -> discoverAsync(true));

        listView.setOnItemClickListener((parent, view, pos, id) -> {
            Api.Msg m = adapter.getItem(pos);
            if ("file".equals(m.kind)) downloadMsg(m);
            else showActions(m);
        });
        listView.setOnItemLongClickListener((parent, view, pos, id) -> {
            showActions(adapter.getItem(pos));
            return true;
        });
    }

    @Override
    protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        handleShareIntent(intent);
    }

    @Override
    protected void onResume() {
        super.onResume();
        String s = prefs.getString(SettingsActivity.KEY_SERVER, "");
        String t = prefs.getString(SettingsActivity.KEY_TOKEN, "");
        if (!s.equals(server) || !t.equals(token)) {
            server = s;
            token = t;
            stopEvents();
            stopPolling();
            messages.clear();
            localMsgs.clear();
            this.progress.clear();
            adapter.notifyDataSetChanged();
            if (server.isEmpty() || token.isEmpty()) showJoin();
            else verifyAndEnter();
        }
    }

    @Override
    protected void onDestroy() {
        running = false;
        stopEvents();
        stopPolling();
        super.onDestroy();
    }

    // ---------- 连接与加入 ----------

    private void verifyAndEnter() {
        final String srv = server, tk = token;
        bg(() -> {
            Api.State result = null;
            Exception failure = null;
            try {
                result = Api.state(srv, tk);
            } catch (Exception e) {
                failure = e;
            }
            final Api.State state = result;
            final Exception err = failure;
            ui.post(() -> {
                if (!running) return;
                if (state != null) enterMain(state);
                else if (isAuthError(err)) {
                    clearToken();
                    showJoin();
                } else {
                    enterMain(null);
                    toast("暂时连不上服务端，正在重连");
                }
            });
        });
    }

    private void enterMain(Api.State state) {
        mainPanel.setVisibility(View.VISIBLE);
        joinPanel.setVisibility(View.GONE);
        if (state != null) applyState(state);
        else subtitle.setText("离线 · 正在重连");
        startEvents();
        startPolling();
        flushPendingShare();
    }

    private void showJoin() {
        stopEvents();
        stopPolling();
        mainPanel.setVisibility(View.GONE);
        joinPanel.setVisibility(View.VISIBLE);
        devicesStrip.setVisibility(View.GONE);
        subtitle.setText("未加入");
        serverInput.setText(server);
        String savedName = prefs.getString(SettingsActivity.KEY_NAME, "");
        nameInput.setText(savedName.isEmpty() ? defaultName() : savedName);
        loadJoinInfo();
    }

    /** 免加入码模式下把「加入码」一栏藏起来。 */
    private void loadJoinInfo() {
        if (server.isEmpty()) return;
        final String srv = server;
        bg(() -> {
            boolean open = false;
            boolean reachable = true;
            try {
                open = Api.openJoin(srv);
            } catch (Exception e) {
                reachable = false;
            }
            final boolean isOpen = open;
            final boolean ok = reachable;
            ui.post(() -> {
                if (!running || !srv.equals(server)) return;
                codeRow.setVisibility(isOpen ? View.GONE : View.VISIBLE);
                if (!ok) joinError.setText("暂时连不上服务端，请检查地址");
            });
        });
    }

    private void doJoin() {
        if (joining) return;
        final String srv = normalize(serverInput.getText().toString());
        final String name = nameInput.getText().toString().trim();
        final String code = codeInput.getText().toString().trim();
        if (srv.isEmpty()) {
            joinError.setText("请填服务地址");
            return;
        }
        if (name.isEmpty()) {
            joinError.setText("请填设备名称");
            return;
        }
        joining = true;
        btnJoin.setEnabled(false);
        joinError.setText("");
        bg(() -> {
            String id = null;
            Exception failure = null;
            try {
                id = Api.join(srv, name, code);
            } catch (Exception e) {
                failure = e;
            }
            final String newToken = id;
            final Exception err = failure;
            ui.post(() -> {
                joining = false;
                btnJoin.setEnabled(true);
                if (!running) return;
                if (newToken == null) {
                    joinError.setText("加入失败：" + errorText(err));
                    return;
                }
                server = srv;
                token = newToken;
                prefs.edit()
                        .putString(SettingsActivity.KEY_SERVER, srv)
                        .putString(SettingsActivity.KEY_TOKEN, newToken)
                        .putString(SettingsActivity.KEY_NAME, name)
                        .apply();
                verifyAndEnter();
            });
        });
    }

    private void clearToken() {
        token = "";
        prefs.edit().remove(SettingsActivity.KEY_TOKEN).apply();
    }

    // ---------- 状态刷新 / SSE ----------

    private void refresh() {
        if (!running || refreshing || server.isEmpty() || token.isEmpty()) return;
        refreshing = true;
        final String srv = server, tk = token;
        bg(() -> {
            Api.State result = null;
            Exception failure = null;
            try {
                result = Api.state(srv, tk);
            } catch (Exception e) {
                failure = e;
            }
            final Api.State state = result;
            final Exception err = failure;
            ui.post(() -> {
                refreshing = false;
                if (!running) return;
                if (state != null) applyState(state);
                else if (isAuthError(err)) {
                    clearToken();
                    showJoin();
                } else {
                    subtitle.setText("离线 · 正在重连");
                }
            });
        });
    }

    private void applyState(Api.State state) {
        myId = state.me == null ? "" : state.me.id;
        messages.clear();
        messages.addAll(state.messages);
        messages.addAll(localMsgs.values());
        adapter.notifyDataSetChanged();
        int online = 0;
        for (Api.Device d : state.devices) if (d.online) online++;
        subtitle.setText(online + " 台在线 · 共 " + state.devices.size() + " 台设备");
        renderDevices(state.devices);
    }

    private void renderDevices(List<Api.Device> devices) {
        devicesBox.removeAllViews();
        if (devices.isEmpty()) {
            TextView t = new TextView(this);
            t.setText("暂无设备");
            t.setTextSize(12);
            t.setTextColor(Color.parseColor("#8b949e"));
            devicesBox.addView(t);
            return;
        }
        for (Api.Device d : devices) {
            TextView t = new TextView(this);
            String label = (d.online ? "● " : "○ ") + d.name;
            if (!d.platform.isEmpty()) label += " · " + d.platform;
            if (d.id.equals(myId)) label += "（本机）";
            t.setText(label);
            t.setTextColor(Color.parseColor(d.online ? "#3fb950" : "#8b949e"));
            t.setTextSize(12);
            t.setBackgroundResource(R.drawable.bg_chip);
            int p = dp(8);
            t.setPadding(p * 2, p, p * 2, p);
            LinearLayout.LayoutParams lp = new LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT);
            lp.setMargins(0, 0, dp(8), 0);
            devicesBox.addView(t, lp);
        }
    }

    private void startEvents() {
        stopEvents();
        if (server.isEmpty() || token.isEmpty()) return;
        final String srv = server, tk = token;
        final Api.EventStream stream = new Api.EventStream();
        eventStream = stream;
        Thread t = new Thread(() -> {
            while (!stream.isCancelled()) {
                Api.events(srv, tk, stream, (type, msg, deleted) ->
                        ui.post(() -> {
                            if (running) refresh();
                        }));
                if (stream.isCancelled()) break;
                try {
                    Thread.sleep(2000);
                } catch (InterruptedException e) {
                    break;
                }
            }
        }, "lanlink-events");
        t.setDaemon(true);
        t.start();
    }

    private void stopEvents() {
        if (eventStream != null) {
            eventStream.close();
            eventStream = null;
        }
    }

    /** SSE 之外再加一层低频兜底，网络切换后一定能恢复。 */
    private void startPolling() {
        stopPolling();
        pollTask = new Runnable() {
            @Override
            public void run() {
                if (!running) return;
                refresh();
                ui.postDelayed(this, 5000);
            }
        };
        ui.postDelayed(pollTask, 5000);
    }

    private void stopPolling() {
        if (pollTask != null) {
            ui.removeCallbacks(pollTask);
            pollTask = null;
        }
    }

    // ---------- 收发 ----------

    private void sendText() {
        final String text = input.getText().toString().trim();
        if (text.isEmpty()) return;
        input.setText("");
        final String srv = server, tk = token;
        bg(() -> {
            Exception failure = null;
            try {
                Api.sendText(srv, tk, text);
            } catch (Exception e) {
                failure = e;
            }
            final Exception err = failure;
            ui.post(() -> {
                if (!running) return;
                if (err != null) {
                    input.setText(text);
                    toast("发送失败：" + errorText(err));
                } else {
                    refresh();
                }
            });
        });
    }

    private void launchFilePicker() {
        Intent intent = new Intent(Intent.ACTION_GET_CONTENT);
        intent.addCategory(Intent.CATEGORY_OPENABLE);
        intent.setType("*/*");
        intent.putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true);
        intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
        try {
            startActivityForResult(intent, REQ_FILES);
        } catch (Exception e) {
            toast("打不开文件选择器");
        }
    }

    @Override
    protected void onActivityResult(int code, int result, Intent data) {
        super.onActivityResult(code, result, data);
        if (code == REQ_FILES && result == RESULT_OK) {
            List<Uri> uris = collectUris(data);
            if (!uris.isEmpty()) uploadUris(uris);
        }
    }

    private List<Uri> collectUris(Intent data) {
        List<Uri> uris = new ArrayList<>();
        if (data == null) return uris;
        ClipData clip = data.getClipData();
        if (clip != null) {
            for (int i = 0; i < clip.getItemCount(); i++) {
                Uri u = clip.getItemAt(i).getUri();
                if (u != null && !uris.contains(u)) uris.add(u);
            }
        }
        if (uris.isEmpty() && data.getData() != null) uris.add(data.getData());
        for (Uri u : uris) grantUriRead(u);
        return uris;
    }

    private void grantUriRead(Uri uri) {
        try {
            grantUriPermission(getPackageName(), uri, Intent.FLAG_GRANT_READ_URI_PERMISSION);
        } catch (Exception ignored) {
        }
    }

    private void uploadUris(List<Uri> uris) {
        if (server.isEmpty() || token.isEmpty()) {
            toast("请先加入 LanLink");
            return;
        }
        final List<Api.Msg> placeholders = new ArrayList<>();
        long total = 0;
        for (Uri u : uris) {
            Api.Msg m = new Api.Msg();
            m.id = "local:" + System.nanoTime() + ":" + placeholders.size();
            m.kind = "file";
            m.name = Api.displayName(this, u);
            m.size = Api.sizeOf(this, u);
            m.fromId = myId;
            m.fromName = "我";
            m.ts = System.currentTimeMillis();
            m.local = true;
            placeholders.add(m);
            localMsgs.put(m.id, m);
            progress.put(m.id, m.size > 0 ? 0 : -1);
            if (m.size <= 0) total = -1;
            else if (total >= 0) total += m.size;
        }
        final long totalBytes = total;
        transferWrap.setVisibility(View.VISIBLE);
        transferBar.setIndeterminate(totalBytes < 0);
        updateTransfer(0, totalBytes, placeholders.size());
        adapter.notifyDataSetChanged();

        final String srv = server, tk = token;
        bg(() -> {
            final long limit = Api.maxBytes(srv);
            int ok = 0, fail = 0;
            long base = 0;
            for (int i = 0; i < uris.size(); i++) {
                final Api.Msg m = placeholders.get(i);
                if (limit > 0 && m.size > limit) {
                    fail++;
                    ui.post(() -> {
                        localMsgs.remove(m.id);
                        progress.remove(m.id);
                        messages.remove(m);
                        adapter.notifyDataSetChanged();
                        toast("超过服务端单文件上限 " + formatSize(limit) + "，已跳过：" + m.name);
                    });
                    continue;
                }
                final long start = base;
                final int[] last = {-2};
                try {
                    Api.upload(getApplicationContext(), srv, tk, uris.get(i), (done, size) -> {
                        int pct = size > 0 ? (int) Math.min(100, done * 100 / size) : -1;
                        if (pct == last[0]) return;
                        last[0] = pct;
                        final int value = pct;
                        ui.post(() -> {
                            progress.put(m.id, value);
                            adapter.notifyDataSetChanged();
                            if (totalBytes > 0) {
                                updateTransfer(start + done, totalBytes, placeholders.size());
                            }
                        });
                    });
                    ok++;
                } catch (Exception e) {
                    fail++;
                    final String reason = errorText(e);
                    ui.post(() -> toast("上传失败：" + m.name + " · " + reason));
                }
                if (m.size > 0) base += m.size;
                ui.post(() -> {
                    localMsgs.remove(m.id);
                    progress.remove(m.id);
                    messages.remove(m);
                    adapter.notifyDataSetChanged();
                });
            }
            final int okCount = ok, failCount = fail;
            ui.post(() -> {
                transferWrap.setVisibility(View.GONE);
                toast(okCount + " 个文件发送成功"
                        + (failCount > 0 ? "，" + failCount + " 个失败" : ""));
                showNotification("批量发送完成", okCount + " 个成功"
                        + (failCount > 0 ? "，" + failCount + " 个失败" : ""), 2001);
                refresh();
            });
        });
    }

    private void updateTransfer(long done, long total, int count) {
        int pct = total > 0 ? (int) Math.min(100, done * 100 / total) : 0;
        transferBar.setProgress(pct);
        transferText.setText(count + " 个文件 · " + (total > 0 ? pct + "%" : "传输中"));
    }

    private void downloadMsg(Api.Msg m) {
        if (progress.containsKey(m.id)) {
            toast("正在传输，请稍候");
            return;
        }
        progress.put(m.id, m.size > 0 ? 0 : -1);
        adapter.notifyDataSetChanged();
        final String srv = server, tk = token;
        bg(() -> {
            Downloader.Target target = null;
            Exception failure = null;
            final int[] last = {-2};
            try {
                target = Downloader.open(getApplicationContext(), prefs, m.name, m.mime);
                Api.download(srv, tk, m.id, m.name, target.out, (done, total) -> {
                    int pct = total > 0 ? (int) Math.min(100, done * 100 / total) : -1;
                    if (pct == last[0]) return;
                    last[0] = pct;
                    final int value = pct;
                    ui.post(() -> {
                        progress.put(m.id, value);
                        adapter.notifyDataSetChanged();
                    });
                });
            } catch (Exception e) {
                failure = e;
            }
            if (target != null) Downloader.finish(getApplicationContext(), target, failure == null);
            final Downloader.Target t = target;
            final Exception err = failure;
            ui.post(() -> {
                progress.remove(m.id);
                adapter.notifyDataSetChanged();
                if (err != null) toast("保存失败：" + errorText(err));
                else {
                    toast("已保存到" + (t == null ? "下载目录" : t.label) + "：" + m.name);
                    showNotification("下载完成", m.name, 2002);
                }
            });
        });
    }

    private void showActions(Api.Msg m) {
        final List<String> labels = new ArrayList<>();
        final List<Runnable> actions = new ArrayList<>();
        if ("file".equals(m.kind)) {
            labels.add("下载 / 保存");
            actions.add(() -> downloadMsg(m));
            labels.add("分享到其他应用");
            actions.add(() -> Sharer.shareFile(this, server, token, m.id, m.name, m.mime));
        } else {
            labels.add("复制文本");
            actions.add(() -> copyText(m.text));
            labels.add("分享到其他应用");
            actions.add(() -> Sharer.shareText(this, m.text));
        }
        if (!m.local && !myId.isEmpty() && m.fromId.equals(myId)) {
            labels.add("删除这条");
            actions.add(() -> deleteMsg(m));
        }
        labels.add("分享服务地址");
        actions.add(() -> Sharer.shareServer(this, server));

        new AlertDialog.Builder(this)
                .setTitle("file".equals(m.kind) ? m.name : "消息")
                .setItems(labels.toArray(new String[0]), (dialog, which) -> actions.get(which).run())
                .show();
    }

    private void copyText(String text) {
        ClipboardManager cm = (ClipboardManager) getSystemService(Context.CLIPBOARD_SERVICE);
        if (cm != null) cm.setPrimaryClip(ClipData.newPlainText("LanLink", text));
        toast("已复制");
    }

    private void deleteMsg(Api.Msg m) {
        final String srv = server, tk = token;
        bg(() -> {
            Exception failure = null;
            try {
                Api.deleteMsg(srv, tk, m.id);
            } catch (Exception e) {
                failure = e;
            }
            final Exception err = failure;
            ui.post(() -> {
                if (err != null) toast("删除失败：" + errorText(err));
                else refresh();
            });
        });
    }

    // ---------- 系统分享：其它 App → LanLink ----------

    private void handleShareIntent(Intent intent) {
        if (intent == null) return;
        String action = intent.getAction();
        boolean single = Intent.ACTION_SEND.equals(action);
        boolean multiple = Intent.ACTION_SEND_MULTIPLE.equals(action);
        if (!single && !multiple) return;
        if (intent.getBooleanExtra(EXTRA_HANDLED, false)) return;
        intent.putExtra(EXTRA_HANDLED, true);

        SharePayload payload = new SharePayload(
                extractSharedText(intent), extractSharedUris(intent, multiple));
        if (!payload.isEmpty()) deliverShare(payload);
    }

    private String extractSharedText(Intent intent) {
        CharSequence extra = intent.getCharSequenceExtra(Intent.EXTRA_TEXT);
        String text = extra == null ? null : extra.toString();
        if (text == null) {
            ClipData clip = intent.getClipData();
            if (clip != null) {
                for (int i = 0; i < clip.getItemCount(); i++) {
                    CharSequence item = clip.getItemAt(i).getText();
                    if (item != null && item.length() > 0) {
                        text = item.toString();
                        break;
                    }
                }
            }
        }
        if (text == null) text = intent.getStringExtra(Intent.EXTRA_SUBJECT);
        return text == null ? null : text.trim();
    }

    private List<Uri> extractSharedUris(Intent intent, boolean multiple) {
        List<Uri> uris = new ArrayList<>();
        ClipData clip = intent.getClipData();
        if (clip != null) {
            for (int i = 0; i < clip.getItemCount(); i++) {
                Uri u = clip.getItemAt(i).getUri();
                if (u != null && !uris.contains(u)) uris.add(u);
            }
        }
        if (multiple) {
            ArrayList<Uri> list = parcelableList(intent, Intent.EXTRA_STREAM);
            if (list != null) {
                for (Uri u : list) if (u != null && !uris.contains(u)) uris.add(u);
            }
        } else {
            Uri one = parcelable(intent, Intent.EXTRA_STREAM);
            if (one != null && !uris.contains(one)) uris.add(one);
        }
        return uris;
    }

    private Uri parcelable(Intent intent, String key) {
        if (Build.VERSION.SDK_INT >= 33) return intent.getParcelableExtra(key, Uri.class);
        return intent.getParcelableExtra(key);
    }

    private ArrayList<Uri> parcelableList(Intent intent, String key) {
        if (Build.VERSION.SDK_INT >= 33) return intent.getParcelableArrayListExtra(key, Uri.class);
        return intent.getParcelableArrayListExtra(key);
    }

    private void deliverShare(SharePayload payload) {
        if (!readyToSend()) {
            pendingShare = payload;
            toast("请先加入 LanLink，加入后会自动发送");
            return;
        }
        sendShare(payload);
    }

    private boolean readyToSend() {
        return !server.isEmpty() && !token.isEmpty();
    }

    private void flushPendingShare() {
        if (pendingShare == null || !readyToSend()) return;
        SharePayload payload = pendingShare;
        pendingShare = null;
        sendShare(payload);
    }

    private void sendShare(SharePayload payload) {
        if (payload.text != null && !payload.text.isEmpty()) {
            final String srv = server, tk = token, text = payload.text;
            bg(() -> {
                boolean ok = true;
                try {
                    Api.sendText(srv, tk, text);
                } catch (Exception e) {
                    ok = false;
                }
                final boolean done = ok;
                ui.post(() -> {
                    toast(done ? "文本已发到 LanLink" : "文本发送失败");
                    refresh();
                });
            });
        }
        if (!payload.uris.isEmpty()) {
            for (Uri u : payload.uris) grantUriRead(u);
            uploadUris(payload.uris);
        }
    }

    // ---------- 通知与自动发现 ----------

    private void askNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33
                && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, REQ_NOTIFICATION);
        }
    }

    @Override
    public void onRequestPermissionsResult(int code, String[] permissions, int[] results) {
        super.onRequestPermissionsResult(code, permissions, results);
    }

    private void showNotification(String title, String text, int id) {
        try {
            NotificationManager nm = (NotificationManager) getSystemService(Context.NOTIFICATION_SERVICE);
            if (nm == null) return;
            Notification.Builder b;
            if (Build.VERSION.SDK_INT >= 26) {
                NotificationChannel ch = new NotificationChannel(CHANNEL, "LanLink",
                        NotificationManager.IMPORTANCE_DEFAULT);
                nm.createNotificationChannel(ch);
                b = new Notification.Builder(this, CHANNEL);
            } else {
                b = new Notification.Builder(this);
            }
            b.setSmallIcon(android.R.drawable.stat_sys_download_done)
                    .setContentTitle(title).setContentText(text).setAutoCancel(true);
            nm.notify(id, b.build());
        } catch (Exception ignored) {
        }
    }

    private void discoverAsync(boolean userInitiated) {
        if (userInitiated) toast("正在寻找 NAS…");
        bg(() -> {
            String found = discover();
            ui.post(() -> {
                if (!running) return;
                if (found != null) {
                    server = found;
                    prefs.edit().putString(SettingsActivity.KEY_SERVER, found).apply();
                    serverInput.setText(found);
                    loadJoinInfo();
                    if (userInitiated) toast("已找到：" + found);
                } else if (userInitiated) {
                    toast("没找到 NAS，请手动填地址");
                }
            });
        });
    }

    /** 广播探测 NAS，被设置页复用。 */
    public static String discover() {
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
                            return "http://" + resp.getAddress().getHostAddress() + ":" + digits;
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

    // ---------- 工具 ----------

    private void bg(Runnable r) {
        Thread t = new Thread(r, "lanlink-net");
        t.setDaemon(true);
        t.start();
    }

    private static String normalize(String raw) {
        String s = raw == null ? "" : raw.trim();
        if (s.isEmpty()) return "";
        if (!s.startsWith("http://") && !s.startsWith("https://")) s = "http://" + s;
        while (s.endsWith("/")) s = s.substring(0, s.length() - 1);
        return s;
    }

    private static boolean isAuthError(Exception e) {
        if (e == null || e.getMessage() == null) return false;
        String m = e.getMessage();
        return m.contains("401") || m.contains("未绑定设备");
    }

    private static String errorText(Exception e) {
        if (e == null || e.getMessage() == null || e.getMessage().isEmpty()) return "连接失败";
        return e.getMessage();
    }

    private String defaultName() {
        String s = (Build.MANUFACTURER + " " + Build.MODEL).trim();
        return s.length() > 20 ? s.substring(0, 20) : s;
    }

    private String appVersion() {
        try {
            return getPackageManager().getPackageInfo(getPackageName(), 0).versionName;
        } catch (Exception e) {
            return "1.2.0";
        }
    }

    private static String formatSize(long n) {
        if (n < 0) return "大小未知";
        if (n < 1024) return n + " B";
        if (n < 1024L * 1024) return String.format(Locale.CHINA, "%.1f KB", n / 1024f);
        if (n < 1024L * 1024 * 1024) return String.format(Locale.CHINA, "%.1f MB", n / (1024f * 1024));
        return String.format(Locale.CHINA, "%.2f GB", n / (1024f * 1024 * 1024));
    }

    private static String formatTime(long ts) {
        return new SimpleDateFormat("MM-dd HH:mm", Locale.CHINA).format(new Date(ts));
    }

    private int dp(int v) {
        return (int) (v * getResources().getDisplayMetrics().density);
    }

    private void toast(String s) {
        Toast.makeText(this, s, Toast.LENGTH_SHORT).show();
    }

    // ---------- 消息列表 ----------

    private class MessageAdapter extends BaseAdapter {
        @Override
        public int getCount() {
            return messages.size();
        }

        @Override
        public Api.Msg getItem(int position) {
            return messages.get(position);
        }

        @Override
        public long getItemId(int position) {
            return messages.get(position).id.hashCode();
        }

        @Override
        public View getView(int position, View convertView, ViewGroup parent) {
            View v = convertView;
            if (v == null) v = getLayoutInflater().inflate(R.layout.item_message, parent, false);

            Api.Msg m = getItem(position);
            LinearLayout row = v.findViewById(R.id.row);
            View bubble = v.findViewById(R.id.bubble);
            TextView sender = v.findViewById(R.id.sender);
            TextView body = v.findViewById(R.id.body);
            TextView time = v.findViewById(R.id.time);
            View fileCard = v.findViewById(R.id.fileCard);
            TextView fileName = v.findViewById(R.id.fileName);
            TextView fileMeta = v.findViewById(R.id.fileMeta);
            RingProgress ring = v.findViewById(R.id.ring);

            boolean mine = !myId.isEmpty() && m.fromId.equals(myId);
            row.setGravity(mine ? Gravity.END : Gravity.START);
            bubble.setBackgroundResource(mine ? R.drawable.bg_bubble_out : R.drawable.bg_bubble_in);
            sender.setText(m.local ? "我 · 发送中" : (m.fromName.isEmpty() ? "未知设备" : m.fromName));
            time.setText(formatTime(m.ts));

            boolean isFile = "file".equals(m.kind);
            body.setVisibility(isFile ? View.GONE : View.VISIBLE);
            fileCard.setVisibility(isFile ? View.VISIBLE : View.GONE);
            if (isFile) {
                fileName.setText(m.name);
                fileMeta.setText(formatSize(m.size) + " · 点击保存 / 长按更多");
                body.setText("");
            } else {
                body.setText(m.text);
            }

            Integer pct = progress.get(m.id);
            ring.setVisibility(pct == null ? View.GONE : View.VISIBLE);
            if (pct != null) ring.setValue(pct);
            return v;
        }
    }
}
