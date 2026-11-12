package com.lanlink.mobile;

import java.util.HashMap;
import java.util.Locale;
import java.util.Map;

/** 常见扩展名的 MIME 查表，替代 android.webkit.MimeTypeMap，避免任何 WebView 相关依赖。 */
final class Mimes {

    private static final Map<String, String> TYPES = new HashMap<>();

    static {
        String[] text = {"txt", "md", "log", "csv", "json", "xml", "yaml", "yml", "ini", "conf", "srt", "ass", "vtt"};
        for (String e : text) TYPES.put(e, "text/plain");
        TYPES.put("html", "text/html");
        TYPES.put("htm", "text/html");
        TYPES.put("css", "text/css");
        TYPES.put("js", "application/javascript");
        TYPES.put("pdf", "application/pdf");
        TYPES.put("doc", "application/msword");
        TYPES.put("docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document");
        TYPES.put("xls", "application/vnd.ms-excel");
        TYPES.put("xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet");
        TYPES.put("ppt", "application/vnd.ms-powerpoint");
        TYPES.put("pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation");
        TYPES.put("zip", "application/zip");
        TYPES.put("rar", "application/x-rar-compressed");
        TYPES.put("7z", "application/x-7z-compressed");
        TYPES.put("tar", "application/x-tar");
        TYPES.put("gz", "application/gzip");
        TYPES.put("apk", "application/vnd.android.package-archive");
        TYPES.put("epub", "application/epub+zip");
        TYPES.put("mobi", "application/x-mobipocket-ebook");
        for (String e : new String[]{"png", "gif", "bmp", "webp", "heic", "ico"}) TYPES.put(e, "image/" + e);
        TYPES.put("jpg", "image/jpeg");
        TYPES.put("jpeg", "image/jpeg");
        TYPES.put("svg", "image/svg+xml");
        for (String e : new String[]{"mp3", "wav", "flac", "aac", "ogg", "m4a", "wma", "opus", "mid"}) TYPES.put(e, "audio/" + e);
        for (String e : new String[]{"mp4", "mkv", "avi", "mov", "webm", "3gp", "flv", "m4v", "wmv", "ts"}) TYPES.put(e, "video/" + e);
    }

    static String guess(String name) {
        if (name == null) return "application/octet-stream";
        int dot = name.lastIndexOf('.');
        if (dot < 0 || dot + 1 >= name.length()) return "application/octet-stream";
        String ext = name.substring(dot + 1).toLowerCase(Locale.ROOT);
        String type = TYPES.get(ext);
        return type == null ? "application/octet-stream" : type;
    }

    private Mimes() {
    }
}
