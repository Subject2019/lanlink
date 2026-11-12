package com.lanlink.mobile;

import android.content.ContentResolver;
import android.content.ContentValues;
import android.content.Context;
import android.content.SharedPreferences;
import android.net.Uri;
import android.os.Build;
import android.os.Environment;
import android.provider.DocumentsContract;
import android.provider.MediaStore;

import java.io.File;
import java.io.FileOutputStream;
import java.io.OutputStream;

/** 为一次下载挑好输出流：授权目录 > 系统「下载」(MediaStore) > 应用私有下载目录。 */
final class Downloader {

    static final class Target {
        OutputStream out;
        Uri uri;
        File file;
        String label = "下载目录";
        boolean pending;   // MediaStore 的 IS_PENDING
        boolean saf;       // SAF 授权目录
    }

    static Target open(Context ctx, SharedPreferences prefs, String name, String mime) throws Exception {
        String type = (mime != null && mime.contains("/")) ? mime : Mimes.guess(name);
        String safe = ShareProvider.safeName(name);
        ContentResolver cr = ctx.getContentResolver();

        String tree = prefs.getString(SettingsActivity.KEY_DOWNLOAD_TREE, null);
        if (tree != null) {
            Uri doc = null;
            try {
                Uri treeUri = Uri.parse(tree);
                String docId = DocumentsContract.getTreeDocumentId(treeUri);
                Uri parent = DocumentsContract.buildDocumentUriUsingTree(treeUri, docId);
                doc = DocumentsContract.createDocument(cr, parent, type, safe);
                if (doc != null) {
                    OutputStream out = cr.openOutputStream(doc);
                    if (out != null) {
                        Target t = new Target();
                        t.out = out;
                        t.uri = doc;
                        t.saf = true;
                        t.label = "授权目录";
                        return t;
                    }
                    DocumentsContract.deleteDocument(cr, doc);
                    doc = null;
                }
            } catch (Exception ignored) {
                if (doc != null) {
                    try {
                        DocumentsContract.deleteDocument(cr, doc);
                    } catch (Exception ignored2) {
                    }
                }
                // 授权失效或该 provider 不支持写入，退回系统下载目录
            }
        }

        if (Build.VERSION.SDK_INT >= 29) {
            ContentValues v = new ContentValues();
            v.put(MediaStore.Downloads.DISPLAY_NAME, safe);
            v.put(MediaStore.Downloads.MIME_TYPE, type);
            v.put(MediaStore.Downloads.IS_PENDING, 1);
            Uri uri = cr.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, v);
            if (uri != null) {
                OutputStream out = cr.openOutputStream(uri);
                if (out != null) {
                    Target t = new Target();
                    t.out = out;
                    t.uri = uri;
                    t.pending = true;
                    t.label = "系统「下载」目录";
                    return t;
                }
                cr.delete(uri, null, null);
            }
        }

        File dir = ctx.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS);
        if (dir == null) dir = new File(ctx.getFilesDir(), "Download");
        //noinspection ResultOfMethodCallIgnored
        dir.mkdirs();
        Target t = new Target();
        t.file = new File(dir, safe);
        t.out = new FileOutputStream(t.file);
        t.label = "应用下载目录";
        return t;
    }

    /** 收尾：成功就结束 MediaStore 的 pending 状态，失败就删掉半成品。 */
    static void finish(Context ctx, Target t, boolean ok) {
        try {
            t.out.close();
        } catch (Exception ignored) {
        }
        if (ok) {
            if (t.pending && t.uri != null) {
                ContentValues v = new ContentValues();
                v.put(MediaStore.Downloads.IS_PENDING, 0);
                try {
                    ctx.getContentResolver().update(t.uri, v, null, null);
                } catch (Exception ignored) {
                }
            }
            return;
        }
        try {
            if (t.saf && t.uri != null) DocumentsContract.deleteDocument(ctx.getContentResolver(), t.uri);
            else if (t.uri != null) ctx.getContentResolver().delete(t.uri, null, null);
            else if (t.file != null) t.file.delete();
        } catch (Exception ignored) {
        }
    }

    private Downloader() {
    }
}
