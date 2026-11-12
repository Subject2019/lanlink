package com.lanlink.mobile;

import android.content.ContentProvider;
import android.content.ContentValues;
import android.content.Context;
import android.database.Cursor;
import android.database.MatrixCursor;
import android.net.Uri;
import android.os.ParcelFileDescriptor;
import android.provider.OpenableColumns;
import android.text.TextUtils;

import java.io.File;
import java.io.FileNotFoundException;

/**
 * 把 cacheDir/share 下待分享的文件以 content:// 暴露给外部 App。
 * 自己实现一个极小的 FileProvider，避免为了单个功能引入 androidx。
 */
public class ShareProvider extends ContentProvider {

    private static final String SUFFIX = ".share";

    private File root;

    static String authority(Context ctx) {
        return ctx.getPackageName() + SUFFIX;
    }

    static File shareDir(Context ctx) {
        File dir = new File(ctx.getCacheDir(), "share");
        //noinspection ResultOfMethodCallIgnored
        dir.mkdirs();
        return dir;
    }

    /** 分享缓存里只允许普通文件名，杜绝路径穿越。 */
    static String safeName(String name) {
        String s = name == null ? "lanlink-file" : name;
        s = s.replace('/', '_').replace('\\', '_')
                .replace('\r', '_').replace('\n', '_').replace('\0', '_');
        if (s.trim().isEmpty() || ".".equals(s) || "..".equals(s)) s = "lanlink-file";
        if (s.length() > 120) s = s.substring(s.length() - 120);
        return s;
    }

    static Uri uriFor(Context ctx, String name, String mime) {
        Uri.Builder b = new Uri.Builder()
                .scheme("content")
                .authority(authority(ctx))
                .appendPath(safeName(name));
        if (mime != null && mime.contains("/")) b.appendQueryParameter("type", mime);
        return b.build();
    }

    @Override
    public boolean onCreate() {
        root = shareDir(getContext());
        return true;
    }

    private File resolve(Uri uri) throws FileNotFoundException {
        String segment = uri.getLastPathSegment();
        if (TextUtils.isEmpty(segment)) throw new FileNotFoundException("empty name");
        File file = new File(root, segment);
        try {
            String base = root.getCanonicalPath();
            String target = file.getCanonicalPath();
            if (!target.startsWith(base + File.separator)) {
                throw new FileNotFoundException("outside share dir");
            }
        } catch (FileNotFoundException e) {
            throw e;
        } catch (Exception e) {
            throw new FileNotFoundException("invalid path");
        }
        if (!file.isFile()) throw new FileNotFoundException("not ready");
        return file;
    }

    @Override
    public ParcelFileDescriptor openFile(Uri uri, String mode) throws FileNotFoundException {
        if (mode != null && !mode.startsWith("r")) {
            throw new FileNotFoundException("read only");
        }
        return ParcelFileDescriptor.open(resolve(uri), ParcelFileDescriptor.MODE_READ_ONLY);
    }

    @Override
    public Cursor query(Uri uri, String[] projection, String selection,
                        String[] selectionArgs, String sortOrder) {
        final File file;
        try {
            file = resolve(uri);
        } catch (FileNotFoundException e) {
            return null;
        }
        String[] cols = projection == null
                ? new String[]{OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE}
                : projection;
        MatrixCursor cursor = new MatrixCursor(cols, 1);
        Object[] row = new Object[cols.length];
        for (int i = 0; i < cols.length; i++) {
            String col = cols[i];
            if (OpenableColumns.DISPLAY_NAME.equals(col)) row[i] = file.getName();
            else if (OpenableColumns.SIZE.equals(col)) row[i] = file.length();
            else if ("_data".equals(col)) row[i] = file.getAbsolutePath();
            else row[i] = null;
        }
        cursor.addRow(row);
        return cursor;
    }

    @Override
    public String getType(Uri uri) {
        String explicit = uri.getQueryParameter("type");
        if (explicit != null && explicit.contains("/")) return explicit;
        return Mimes.guess(uri.getLastPathSegment());
    }

    @Override
    public Uri insert(Uri uri, ContentValues values) {
        return null;
    }

    @Override
    public int delete(Uri uri, String selection, String[] selectionArgs) {
        return 0;
    }

    @Override
    public int update(Uri uri, ContentValues values, String selection, String[] selectionArgs) {
        return 0;
    }
}
