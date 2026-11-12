package com.lanlink.mobile;

import android.content.Context;
import android.database.Cursor;
import android.net.Uri;
import android.provider.OpenableColumns;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLEncoder;
import java.util.ArrayList;
import java.util.List;

/**
 * 直连服务端的网络层：不经过 WebView，自己带 lanlink_token 发请求。
 * 所有方法都是阻塞式，调用方负责放到后台线程。
 */
public class Api {

    static final String COOKIE = "lanlink_token";

    public static class Msg {
        public String id = "";
        public String kind = "text";
        public String text = "";
        public String fromId = "";
        public String fromName = "";
        public long ts = 0;
        public String name = "";
        public long size = 0;
        public String mime = "";
        public boolean local = false; // 上传时先在列表里占位
    }

    public static class Device {
        public String id = "";
        public String name = "";
        public String platform = "";
        public boolean online;
    }

    public static class State {
        public final List<Msg> messages = new ArrayList<>();
        public final List<Device> devices = new ArrayList<>();
        public Device me;
    }

    public interface Progress {
        void onProgress(long done, long total);
    }

    public interface EventListener {
        void onEvent(String type, Msg msg, String deleteId);
    }

    // ---------- 基础请求 ----------

    private static HttpURLConnection open(String url, String method, String token) throws Exception {
        HttpURLConnection c = (HttpURLConnection) new URL(url).openConnection();
        c.setRequestMethod(method);
        c.setConnectTimeout(8000);
        c.setReadTimeout(20000);
        if (token != null && !token.isEmpty()) {
            c.setRequestProperty("Cookie", COOKIE + "=" + token);
        }
        return c;
    }

    private static void check(HttpURLConnection c) throws Exception {
        int code = c.getResponseCode();
        if (code >= 200 && code < 300) return;
        String msg = "HTTP " + code;
        try {
            InputStream err = c.getErrorStream();
            if (err != null) {
                String raw = readAll(err);
                String detail = new JSONObject(raw).optString("error", "");
                if (!detail.isEmpty()) msg = detail;
            }
        } catch (Exception ignored) {
        }
        throw new Exception(msg);
    }

    private static String readAll(InputStream in) throws Exception {
        // 先收齐字节再整体解码，否则一个汉字跨两个读取块时会出现乱码
        ByteArrayOutputStream all = new ByteArrayOutputStream();
        byte[] buf = new byte[4096];
        int n;
        while ((n = in.read(buf)) > 0) all.write(buf, 0, n);
        in.close();
        return new String(all.toByteArray(), "UTF-8");
    }

    private static String body(HttpURLConnection c) throws Exception {
        return readAll(c.getInputStream());
    }

    private static void writeJSON(HttpURLConnection c, JSONObject o) throws Exception {
        c.setDoOutput(true);
        c.setRequestProperty("Content-Type", "application/json; charset=utf-8");
        OutputStream out = c.getOutputStream();
        out.write(o.toString().getBytes("UTF-8"));
        out.close();
    }

    // ---------- 加入与状态 ----------

    /** 是否免加入码。 */
    public static boolean openJoin(String server) throws Exception {
        HttpURLConnection c = open(server + "/api/info", "GET", null);
        try {
            check(c);
            return new JSONObject(body(c)).optBoolean("open", false);
        } finally {
            c.disconnect();
        }
    }

    /** 服务端单文件上限（字节）；旧服务端没有这个字段或网络不通时返回 0，表示不预检。 */
    public static long maxBytes(String server) {
        HttpURLConnection c = null;
        try {
            c = open(server + "/api/info", "GET", null);
            check(c);
            return new JSONObject(body(c)).optLong("maxBytes", 0);
        } catch (Exception ignored) {
            return 0;
        } finally {
            if (c != null) c.disconnect();
        }
    }

    /** 返回设备令牌（服务端把 device.id 当 cookie 值用）。 */
    public static String join(String server, String name, String code) throws Exception {
        HttpURLConnection c = open(server + "/api/join", "POST", null);
        try {
            JSONObject b = new JSONObject();
            b.put("name", name);
            b.put("code", code);
            b.put("platform", "Android");
            writeJSON(c, b);
            check(c);
            String id = new JSONObject(body(c)).getJSONObject("device").optString("id", "");
            if (id.isEmpty()) throw new Exception("加入成功但没拿到令牌");
            return id;
        } finally {
            c.disconnect();
        }
    }

    public static State state(String server, String token) throws Exception {
        HttpURLConnection c = open(server + "/api/state", "GET", token);
        try {
            check(c);
            JSONObject o = new JSONObject(body(c));
            State s = new State();
            s.me = parseDevice(o.optJSONObject("me"));
            JSONArray msgs = o.optJSONArray("messages");
            if (msgs != null) {
                for (int i = 0; i < msgs.length(); i++) s.messages.add(parseMsg(msgs.getJSONObject(i)));
            }
            JSONArray devs = o.optJSONArray("devices");
            if (devs != null) {
                for (int i = 0; i < devs.length(); i++) s.devices.add(parseDevice(devs.getJSONObject(i)));
            }
            return s;
        } finally {
            c.disconnect();
        }
    }

    public static Msg sendText(String server, String token, String text) throws Exception {
        HttpURLConnection c = open(server + "/api/messages", "POST", token);
        try {
            JSONObject b = new JSONObject();
            b.put("text", text);
            writeJSON(c, b);
            check(c);
            return parseMsg(new JSONObject(body(c)));
        } finally {
            c.disconnect();
        }
    }

    public static void rename(String server, String token, String name) throws Exception {
        HttpURLConnection c = open(server + "/api/device", "POST", token);
        try {
            JSONObject b = new JSONObject();
            b.put("name", name);
            writeJSON(c, b);
            check(c);
        } finally {
            c.disconnect();
        }
    }

    public static void deleteMsg(String server, String token, String id) throws Exception {
        HttpURLConnection c = open(server + "/api/messages/" + URLEncoder.encode(id, "UTF-8"),
                "DELETE", token);
        try {
            check(c);
        } finally {
            c.disconnect();
        }
    }

    // ---------- 文件传输 ----------

    /** 流式上传，边读边回调字节进度。 */
    public static Msg upload(Context ctx, String server, String token, Uri uri, Progress progress)
            throws Exception {
        String name = displayName(ctx, uri);
        long size = sizeOf(ctx, uri);
        HttpURLConnection c = open(server + "/api/files?name=" + URLEncoder.encode(name, "UTF-8"),
                "PUT", token);
        try {
            c.setDoOutput(true);
            c.setUseCaches(false);
            c.setReadTimeout(60000);
            if (size > 0) {
                // 带 Content-Length，服务端才能在写盘前检查剩余空间并返回「存储空间不足」
                c.setFixedLengthStreamingMode(size);
            } else {
                c.setChunkedStreamingMode(64 * 1024);
            }
            c.setRequestProperty("Content-Type", "application/octet-stream");
            String mime = ctx.getContentResolver().getType(uri);
            if (mime != null) c.setRequestProperty("X-File-Type", mime);

            InputStream in = ctx.getContentResolver().openInputStream(uri);
            if (in == null) throw new Exception("打不开文件：" + name);
            try (OutputStream out = c.getOutputStream()) {
                byte[] buf = new byte[64 * 1024];
                long done = 0;
                int n;
                while ((n = in.read(buf)) > 0) {
                    out.write(buf, 0, n);
                    done += n;
                    if (progress != null) progress.onProgress(done, size);
                }
                out.flush();
            } finally {
                in.close();
            }
            check(c);
            return parseMsg(new JSONObject(body(c)));
        } finally {
            c.disconnect();
        }
    }

    /** 流式下载到调用方给的输出流，边写边回调字节进度；输出流由调用方关闭。 */
    public static void download(String server, String token, String id, String name,
                                OutputStream out, Progress progress) throws Exception {
        HttpURLConnection c = open(server + "/api/files/" + URLEncoder.encode(id, "UTF-8")
                + "?n=" + URLEncoder.encode(name, "UTF-8"), "GET", token);
        try {
            c.setReadTimeout(60000);
            check(c);
            long total = c.getContentLengthLong();
            InputStream in = c.getInputStream();
            try {
                byte[] buf = new byte[64 * 1024];
                long done = 0;
                int n;
                while ((n = in.read(buf)) > 0) {
                    out.write(buf, 0, n);
                    done += n;
                    if (progress != null) progress.onProgress(done, total);
                }
                out.flush();
            } finally {
                in.close();
            }
        } finally {
            c.disconnect();
        }
    }

    /**
     * SSE 长连接句柄。Activity 退出时 close()：读循环会在最多一个读超时内退出，
     * 同时断开底层连接，避免设备一直被服务端标记为在线。
     */
    public static final class EventStream {
        private volatile boolean cancelled;
        private HttpURLConnection conn;
        private final Object lock = new Object();

        public boolean isCancelled() {
            return cancelled;
        }

        public void close() {
            cancelled = true;
            HttpURLConnection c;
            synchronized (lock) {
                c = conn;
                conn = null;
            }
            if (c != null) c.disconnect();
        }

        private boolean attach(HttpURLConnection c) {
            synchronized (lock) {
                if (cancelled) return false;
                conn = c;
                return true;
            }
        }

        private void detach() {
            synchronized (lock) {
                conn = null;
            }
        }
    }

    /** SSE 长连接：让服务端把设备标记为在线，并即时推送新消息。断开后由调用方重连。 */
    public static void events(String server, String token, EventStream stream, EventListener listener) {
        HttpURLConnection c = null;
        try {
            c = (HttpURLConnection) new URL(server + "/api/events").openConnection();
            c.setRequestMethod("GET");
            c.setConnectTimeout(8000);
            c.setReadTimeout(30000); // 服务端每 25 秒发 ping；超时就交给外层重连
            if (token != null && !token.isEmpty()) {
                c.setRequestProperty("Cookie", COOKIE + "=" + token);
            }
            c.setRequestProperty("Accept", "text/event-stream");
            if (!stream.attach(c) || c.getResponseCode() != 200) return;

            BufferedReader reader = new BufferedReader(new InputStreamReader(c.getInputStream(), "UTF-8"));
            String line;
            while (!stream.isCancelled() && (line = reader.readLine()) != null) {
                if (!line.startsWith("data: ")) continue;
                try {
                    JSONObject ev = new JSONObject(line.substring(6));
                    String type = ev.optString("type");
                    Msg m = null;
                    String deleted = null;
                    JSONObject data = ev.optJSONObject("data");
                    if ("message".equals(type) && data != null) m = parseMsg(data);
                    else if ("delete".equals(type) && data != null) deleted = data.optString("id");
                    if (!stream.isCancelled()) listener.onEvent(type, m, deleted);
                } catch (Exception ignored) {
                }
            }
        } catch (Exception ignored) {
            // 断网、超时、被 close() 断开：都由外层循环决定是否重连
        } finally {
            stream.detach();
            if (c != null) c.disconnect();
        }
    }

    // ---------- JSON / Uri 工具 ----------

    private static Msg parseMsg(JSONObject o) {
        Msg m = new Msg();
        m.id = o.optString("id", "");
        m.kind = o.optString("kind", "text");
        m.text = o.optString("text", "");
        m.fromId = o.optString("fromId", "");
        m.fromName = o.optString("fromName", "");
        m.ts = o.optLong("ts", System.currentTimeMillis());
        JSONObject f = o.optJSONObject("file");
        if (f != null) {
            m.name = f.optString("name", "文件");
            m.size = f.optLong("size", 0);
            m.mime = f.optString("mime", "");
        }
        return m;
    }

    private static Device parseDevice(JSONObject o) {
        if (o == null) return null;
        Device d = new Device();
        d.id = o.optString("id", "");
        d.name = o.optString("name", "");
        d.platform = o.optString("platform", "");
        d.online = o.optBoolean("online", false);
        return d;
    }

    static String displayName(Context ctx, Uri uri) {
        String result = null;
        try (Cursor c = ctx.getContentResolver().query(uri, null, null, null, null)) {
            if (c != null && c.moveToFirst()) {
                int i = c.getColumnIndex(OpenableColumns.DISPLAY_NAME);
                if (i >= 0) result = c.getString(i);
            }
        } catch (Exception ignored) {
        }
        if (result == null || result.trim().isEmpty()) result = uri.getLastPathSegment();
        if (result == null || result.trim().isEmpty()) result = "file-" + System.currentTimeMillis();
        return result.replace('/', '_').replace('\\', '_');
    }

    static long sizeOf(Context ctx, Uri uri) {
        try (Cursor c = ctx.getContentResolver().query(uri, null, null, null, null)) {
            if (c != null && c.moveToFirst()) {
                int i = c.getColumnIndex(OpenableColumns.SIZE);
                if (i >= 0 && !c.isNull(i)) return c.getLong(i);
            }
        } catch (Exception ignored) {
        }
        return -1;
    }
}
