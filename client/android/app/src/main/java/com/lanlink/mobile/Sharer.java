package com.lanlink.mobile;

import android.app.Activity;
import android.content.ClipData;
import android.content.Context;
import android.content.Intent;
import android.net.Uri;
import android.widget.Toast;

import java.io.File;
import java.io.FileOutputStream;
import java.io.OutputStream;

/** 把 LanLink 里的文本 / 文件 / 服务地址通过系统分享面板发到其它 App。 */
public class Sharer {

    static void shareText(Activity activity, String text) {
        if (text == null || text.trim().isEmpty()) return;
        Intent intent = new Intent(Intent.ACTION_SEND);
        intent.setType("text/plain");
        intent.putExtra(Intent.EXTRA_TEXT, text);
        intent.putExtra(Intent.EXTRA_SUBJECT, "来自 LanLink");
        launch(activity, intent, "分享到");
    }

    /** 把自己的 NAS 地址发出去，别人点开就能加入互传。 */
    static void shareServer(Activity activity, String server) {
        if (server == null || server.isEmpty()) {
            toast(activity, "还没设置服务器地址");
            return;
        }
        shareText(activity, "用 LanLink 连到我的局域网互传：" + server
                + "\n（需在同一局域网，打开后填设备名即可加入）");
    }

    /** 文件在 NAS 上，先用原生网络层下载到缓存，再由 ShareProvider 以 content:// 分享出去。 */
    static void shareFile(Activity activity, String server, String token,
                          String id, String name, String mime) {
        final String displayName = name == null || name.isEmpty() ? "lanlink-file" : name;
        final String fileName = ShareProvider.safeName(displayName);
        final String type = (mime == null || !mime.contains("/")) ? Mimes.guess(fileName) : mime;
        toast(activity, "正在准备「" + displayName + "」…");

        new Thread(() -> {
            File dst = new File(ShareProvider.shareDir(activity), fileName);
            boolean ok;
            try (OutputStream out = new FileOutputStream(dst)) {
                Api.download(server, token, id, displayName, out, null);
                ok = true;
            } catch (Exception e) {
                //noinspection ResultOfMethodCallIgnored
                dst.delete();
                ok = false;
            }
            final boolean prepared = ok;
            activity.runOnUiThread(() -> {
                if (activity.isFinishing()) return;
                if (!prepared) {
                    toast(activity, "文件准备失败，请检查网络后重试");
                    return;
                }
                Uri content = ShareProvider.uriFor(activity, fileName, type);
                Intent intent = new Intent(Intent.ACTION_SEND);
                intent.setType(type);
                intent.putExtra(Intent.EXTRA_STREAM, content);
                intent.putExtra(Intent.EXTRA_SUBJECT, displayName);
                // ClipData 让 Android 10+ 把读权限正确授予最终接收方
                intent.setClipData(ClipData.newRawUri(displayName, content));
                intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
                Intent chooser = Intent.createChooser(intent, "分享 " + displayName);
                chooser.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
                try {
                    activity.startActivity(chooser);
                } catch (Exception e) {
                    toast(activity, "没有可用的分享应用");
                }
            });
        }, "lanlink-share").start();
    }

    private static void launch(Activity activity, Intent intent, String title) {
        try {
            activity.startActivity(Intent.createChooser(intent, title));
        } catch (Exception e) {
            toast(activity, "没有可用的分享应用");
        }
    }

    private static void toast(Context ctx, String text) {
        Toast.makeText(ctx, text, Toast.LENGTH_SHORT).show();
    }
}
