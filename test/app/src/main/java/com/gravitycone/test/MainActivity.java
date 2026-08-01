package com.gravitycone.test;

import android.content.Context;
import android.content.Intent;
import android.net.VpnService;
import android.net.wifi.WifiManager;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.os.ParcelFileDescriptor;
import android.text.TextUtils;
import android.util.Log;
import android.widget.Button;
import android.widget.EditText;
import android.widget.ScrollView;
import android.widget.TextView;
import android.widget.Toast;

import androidx.annotation.NonNull;
import androidx.appcompat.app.AppCompatActivity;

import net.gravitycone.ffi.GravityConeAndroidAPI;

import org.json.JSONException;
import org.json.JSONObject;

import java.io.IOException;
import java.io.Reader;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Date;
import java.util.List;
import java.util.Locale;

/**
 * GravityCone FFI 测试界面（基岩版 / PaperConnect）。
 *
 * <p>功能：初始化引擎 → 创建房间（主机，paperconnect 协议）/ 加入房间（客人，
 * P/ 房间码）/ 校验房间码 / 断开连接，500ms 轮询状态 JSON，1s 轮询引擎日志。</p>
 *
 * <p>VpnService 请求由 EasyTier 在需要 TUN 时通过 JNI 回调触发，本应用自动接受
 * （直接 establish 并注入 fd）。若系统弹出 VPN 确认对话框（Android 14+），
 * 点"允许"即可。</p>
 */
public class MainActivity extends AppCompatActivity {

    // ===== [调试标记] 2026-07-31：此注释用于验证源码变更是否被编入 APK =====
    private static final String TAG = "GravityConeTest";

    /** 状态轮询间隔（ms），与 SDK 建议一致。 */
    private static final long STATE_POLL_INTERVAL = 500;
    /** 引擎日志轮询间隔（ms）。 */
    private static final long LOG_POLL_INTERVAL = 1000;
    /** 操作日志保留的最大行数。 */
    private static final int MAX_OP_LOG_LINES = 300;
    /** 引擎日志在界面中保留的最大字符数（从头截断）。 */
    private static final int MAX_ENGINE_LOG_CHARS = 50000;

    private final Handler mainHandler = new Handler(Looper.getMainLooper());
    private final List<String> opLogLines = new ArrayList<>();

    // ---- 视图 ----
    private TextView tvMeta;
    private TextView tvState;
    private TextView tvStateJson;
    private TextView tvOpLog;
    private TextView tvEngineLog;
    private EditText etPlayer;
    private EditText etRoom;
    private Button btnInit;
    private Button btnShutdown;
    private Button btnHost;
    private Button btnGuest;
    private Button btnVerify;
    private Button btnWaiting;
    private ScrollView scrollOpLog;
    private ScrollView scrollEngineLog;

    // ---- 引擎状态 ----
    private boolean initialized = false;
    private int lastStateIndex = -1;
    private String lastEngineLog = "";

    /** VPN 授权请求码（VpnService.prepare）。 */
    private static final int REQ_VPN_AUTH = 1001;
    /** 用户是否已授予 VPN 权限（未授权时 establish() 必然失败）。 */
    private boolean vpnAuthorized = false;


    // =====================================================================
    // 生命周期
    // =====================================================================

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);

        tvMeta = findViewById(R.id.tv_meta);
        tvState = findViewById(R.id.tv_state);
        tvStateJson = findViewById(R.id.tv_state_json);
        tvOpLog = findViewById(R.id.tv_op_log);
        tvEngineLog = findViewById(R.id.tv_engine_log);
        etPlayer = findViewById(R.id.et_player);
        etRoom = findViewById(R.id.et_room);
        btnInit = findViewById(R.id.btn_init);
        btnShutdown = findViewById(R.id.btn_shutdown);
        btnHost = findViewById(R.id.btn_host);
        btnGuest = findViewById(R.id.btn_guest);
        btnVerify = findViewById(R.id.btn_verify);
        btnWaiting = findViewById(R.id.btn_waiting);
        scrollOpLog = findViewById(R.id.scroll_op_log);
        scrollEngineLog = findViewById(R.id.scroll_engine_log);

        btnInit.setOnClickListener(v -> {
            requestVpnPermission();
            doInit();
        });
        btnShutdown.setOnClickListener(v -> doShutdown());
        btnHost.setOnClickListener(v -> doHost());
        btnGuest.setOnClickListener(v -> doGuest());
        btnVerify.setOnClickListener(v -> doVerify());
        btnWaiting.setOnClickListener(v -> doWaiting());

        updateButtons();
        appendOpLog("应用启动，点击「初始化引擎」开始");

        // 先启动 VpnService 获取 attach 实例（Builder 必须通过实例创建），
        // EasyTier 的 TUN 请求发生在引擎初始化之后，此时实例已就绪。
        startService(new Intent(this, GravityConeVpnService.class));

        acquireMulticastLock();

        startPollers();
    }

    /**
     * 持有 MulticastLock：基岩版的房间发现（NetherNet 7551 广播）和
     * RakNet fake server 的 ping 响应都依赖收 UDP 广播/多播，
     * Android 上不持锁收不到。
     */
    private void acquireMulticastLock() {
        try {
            WifiManager wifi = (WifiManager) getSystemService(Context.WIFI_SERVICE);
            if (wifi != null) {
                WifiManager.MulticastLock lock = wifi.createMulticastLock("gravitycone-lan");
                lock.setReferenceCounted(false);
                lock.acquire();
                Log.i(TAG, "MulticastLock acquired (LAN broadcast receive enabled)");
                appendOpLog("MulticastLock 已获取（局域网广播可用）");
            } else {
                Log.e(TAG, "WifiManager is null — cannot acquire MulticastLock");
                appendOpLog("获取 MulticastLock 失败：WifiManager 为空");
            }
        } catch (Throwable t) {
            Log.e(TAG, "MulticastLock acquire failed", t);
            appendOpLog("获取 MulticastLock 失败：" + t);
        }
    }

    @Override
    protected void onDestroy() {
        super.onDestroy();
        mainHandler.removeCallbacks(statePoller);
        mainHandler.removeCallbacks(logPoller);
    }

    // =====================================================================
    // 引擎操作
    // =====================================================================

    /**
     * 申请 VPN 权限（VpnService.prepare）。
     *
     * <p>Android 11+ 上未授权时 {@code VpnService.Builder.establish()} 会直接抛
     * SecurityException——必须先通过 prepare 弹授权框。授权结果在
     * {@link #onActivityResult} 里记录。</p>
     */
    private void requestVpnPermission() {
        try {
            Intent prepare = VpnService.prepare(this);
            if (prepare != null) {
                startActivityForResult(prepare, REQ_VPN_AUTH);
            } else {
                vpnAuthorized = true;
            }
        } catch (Throwable t) {
            appendOpLog("VPN 权限检查异常：" + t);
        }
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (requestCode == REQ_VPN_AUTH) {
            vpnAuthorized = resultCode == RESULT_OK;
            appendOpLog(vpnAuthorized
                    ? "VPN 授权成功 ✓（之后创建/加入房间会自动建立 VPN）"
                    : "VPN 授权被拒绝——不授权将无法联机");
        }
    }

    /** 初始化引擎：启动 Go 运行时 + EasyTier（进程内）。 */
    private void doInit() {
        if (initialized) {
            toast("引擎已初始化");
            return;
        }
        try {
            GravityConeAndroidAPI.Metadata meta = GravityConeAndroidAPI.initialize(this, vpnServiceCallback);
            initialized = true;
            lastStateIndex = -1;
            tvMeta.setText("引擎已初始化 — GravityCone " + meta.getGravityconeVersion()
                    + " | EasyTier " + meta.getEasyTierVersion());
            appendOpLog("引擎初始化成功：GravityCone " + meta.getGravityconeVersion()
                    + "，EasyTier " + meta.getEasyTierVersion());
        } catch (Throwable t) {
            appendOpLog("引擎初始化失败：" + t);

            toast("初始化失败：" + t.getMessage());
        }
        updateButtons();
    }

    /** 关闭引擎：停止所有房间/连接，释放资源。 */
    private void doShutdown() {
        try {
            GravityConeAndroidAPI.shutdown();
        } catch (Throwable t) {
            appendOpLog("关闭引擎异常：" + t);
        }
        initialized = false;
        lastStateIndex = -1;
        tvMeta.setText("引擎：未初始化");
        tvState.setText("—");
        tvStateJson.setText("—");
        appendOpLog("引擎已关闭");
        updateButtons();
    }

    /** 创建房间（主机）：扫描本机基岩版服务器，生成 P/ 房间码。 */
    private void doHost() {
        String player = playerName();
        try {
            GravityConeAndroidAPI.setScanning("", player, "paperconnect");
            appendOpLog("开始托管（基岩版），玩家名：" + player);
        } catch (Throwable t) {
            appendOpLog("托管失败：" + t);
        }
    }

    /** 加入房间（客人）：通过 P/ 房间码连接到主机。 */
    private void doGuest() {
        String room = etRoom.getText().toString().trim();
        if (room.isEmpty()) {
            toast("请输入房间码");
            return;
        }
        String player = playerName();
        try {
            boolean ok = GravityConeAndroidAPI.setGuesting(room, player);
            appendOpLog(ok
                    ? "开始加入房间 " + room + "，玩家名：" + player
                    : "加入失败：房间码无效或引擎未处于空闲状态");
            if (!ok) {
                toast("加入失败：房间码无效或引擎未空闲");
            }
        } catch (Throwable t) {
            appendOpLog("加入异常：" + t);
        }
    }

    /** 校验房间码类型（不发起连接）。 */
    private void doVerify() {
        String room = etRoom.getText().toString().trim();
        if (room.isEmpty()) {
            toast("请输入房间码");
            return;
        }
        try {
            GravityConeAndroidAPI.RoomType type = GravityConeAndroidAPI.parseRoomCode(room);
            String desc;
            if (type == null) {
                desc = "无效房间码";
            } else if (type == GravityConeAndroidAPI.RoomType.PAPER_CONNECT) {
                desc = "PaperConnect（基岩版）✓";
            } else {
                desc = "Scaffolding（Java 版）";
            }
            appendOpLog("房间码校验 [" + room + "] → " + desc);
            toast(desc);
        } catch (Throwable t) {
            appendOpLog("校验异常：" + t);
        }
    }

    /** 断开当前房间/连接，回到空闲状态。 */
    private void doWaiting() {
        try {
            GravityConeAndroidAPI.setWaiting();
            appendOpLog("已断开连接，回到空闲状态");
        } catch (Throwable t) {
            appendOpLog("断开异常：" + t);
        }
    }

    // =====================================================================
    // VpnService 回调（由 native 线程触发）
    // =====================================================================

    /**
     * EasyTier 请求 TUN 设备时被调用。native 侧会阻塞等待 fd（30 秒超时），
     * 因此直接在当前线程建立 VPN 并立即返回，不弹窗确认。
     */
    private final GravityConeAndroidAPI.VpnServiceCallback vpnServiceCallback =
            new GravityConeAndroidAPI.VpnServiceCallback() {
                @Override
                public void onStartVpnService() {
                    try {
                        GravityConeAndroidAPI.VpnServiceRequest request =
                                GravityConeAndroidAPI.getPendingVpnServiceRequest();
                        // Builder 是 VpnService 的非静态内部类，必须通过实例创建
                        GravityConeVpnService svc = GravityConeVpnService.awaitInstance(2000);
                        ParcelFileDescriptor fd = svc.establishVpn(request);
                        appendOpLog("VPN 已建立或复用（自动接受），TUN fd=" + fd.getFd());
                    } catch (Throwable t) {
                        appendOpLog("VPN 建立失败：" + t);
                        // 解除 native 侧的阻塞等待
                        try {
                            GravityConeAndroidAPI.getPendingVpnServiceRequest().reject();
                        } catch (Throwable ignore) {
                            // 可能已超时清除
                        }
                    }
                }
            };

    // =====================================================================
    // 轮询
    // =====================================================================

    private void startPollers() {
        mainHandler.postDelayed(statePoller, STATE_POLL_INTERVAL);
        mainHandler.postDelayed(logPoller, LOG_POLL_INTERVAL);
    }

    /** 轮询状态 JSON 并更新界面。 */
    private final Runnable statePoller = new Runnable() {
        @Override
        public void run() {
            if (initialized) {
                pollState();
            }
            mainHandler.postDelayed(this, STATE_POLL_INTERVAL);
        }
    };

    /** 轮询引擎日志（application.log）。 */
    private final Runnable logPoller = new Runnable() {
        @Override
        public void run() {
            if (initialized) {
                pollEngineLog();
            }
            mainHandler.postDelayed(this, LOG_POLL_INTERVAL);
        }
    };

    private void pollState() {
        String raw;
        try {
            raw = GravityConeAndroidAPI.getState();
        } catch (Throwable t) {
            return;
        }
        if (raw == null) {
            return;
        }

        try {
            JSONObject obj = new JSONObject(raw);
            int index = obj.optInt("index", -1);
            String state = obj.optString("state", "");

            if (index != lastStateIndex) {
                lastStateIndex = index;
                String detail = obj.optString("error", "");
                appendOpLog("状态变化 #" + index + " → " + state
                        + (detail.isEmpty() ? "" : "（" + detail + "）"));
            }

            String friendly = formatState(obj);
            tvState.setText(friendly);
        } catch (JSONException e) {
            tvState.setText("状态解析失败");
        }
        tvStateJson.setText(raw);
    }

    /** 把状态 JSON 转成人类可读文本（基岩版字段）。 */
    @NonNull
    private static String formatState(JSONObject obj) throws JSONException {
        String state = obj.optString("state", "");
        switch (state) {
            case "waiting":
                return "空闲（waiting）";
            case "host-scanning":
                return "主机：正在扫描本机服务器…";
            case "host-starting":
                return "主机：启动中…\n房间码：" + obj.optString("room", "—");
            case "host-ok":
                return "主机就绪 ✓\n"
                        + "房间码：" + obj.optString("room", "—") + "\n"
                        + "子协议：" + obj.optString("sub_protocol", "—") + "\n"
                        + "游戏端口：" + obj.optInt("game_port", 0);
            case "guest-connecting":
                return "客机：连接中…\n房间：" + obj.optString("room", "—")
                        + "（" + obj.optString("step", "") + "）";
            case "guest-ok":
                return "客机就绪 ✓\n"
                        + "连接地址：" + obj.optString("url", "—") + "\n"
                        + "子协议：" + obj.optString("sub_protocol", "—");
            case "exception":
                // 新版本 SDK 的 exception 状态带 error 字段（失败原因），旧版 SDK 只有 type
                String err = obj.optString("error", "");
                return "出错 ❌（type=" + obj.optInt("type", -1) + "）"
                        + (err.isEmpty() ? "" : "\n" + err);
            default:
                return state;
        }
    }

    /** 读取并展示引擎日志（collectLogs 每次从头读全量）。 */
    private void pollEngineLog() {
        Reader reader = null;
        try {
            reader = GravityConeAndroidAPI.collectLogs();
            StringBuilder sb = new StringBuilder();
            char[] buf = new char[4096];
            int n;
            while ((n = reader.read(buf)) > 0) {
                sb.append(buf, 0, n);
            }
            String text = sb.toString();
            if (!text.equals(lastEngineLog)) {
                lastEngineLog = text;
                if (text.length() > MAX_ENGINE_LOG_CHARS) {
                    text = text.substring(text.length() - MAX_ENGINE_LOG_CHARS);
                }
                tvEngineLog.setText(text);
                scrollEngineLog.post(() ->
                        scrollEngineLog.fullScroll(ScrollView.FOCUS_DOWN));
            }
        } catch (IOException | RuntimeException e) {
            // 日志读取失败不影响主流程
        } finally {
            if (reader != null) {
                try {
                    reader.close();
                } catch (IOException ignore) {
                    // 忽略
                }
            }
        }
    }

    // =====================================================================
    // 界面辅助
    // =====================================================================

    private void updateButtons() {
        btnInit.setEnabled(!initialized);
        btnShutdown.setEnabled(initialized);
        btnHost.setEnabled(initialized);
        btnGuest.setEnabled(initialized);
        btnVerify.setEnabled(initialized);
        btnWaiting.setEnabled(initialized);
    }

    private String playerName() {
        String name = etPlayer.getText().toString().trim();
        return name.isEmpty() ? "Player" : name;
    }

    /** 追加操作日志（可跨线程调用，自动切换到主线程）。 */
    private void appendOpLog(String line) {
        if (Looper.myLooper() != Looper.getMainLooper()) {
            mainHandler.post(() -> appendOpLog(line));
            return;
        }
        String ts = new SimpleDateFormat("HH:mm:ss", Locale.US).format(new Date());
        opLogLines.add("[" + ts + "] " + line);
        while (opLogLines.size() > MAX_OP_LOG_LINES) {
            opLogLines.remove(0);
        }
        tvOpLog.setText(TextUtils.join("\n", opLogLines));
        scrollOpLog.post(() -> scrollOpLog.fullScroll(ScrollView.FOCUS_DOWN));
    }

    private void toast(String msg) {
        Toast.makeText(this, msg, Toast.LENGTH_SHORT).show();
    }
}
