package net.gravitycone.ffi;

import android.content.Context;
import android.net.VpnService;
import android.os.ParcelFileDescriptor;
import android.util.Log;

import androidx.annotation.Nullable;

import java.io.BufferedReader;
import java.io.File;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.RandomAccessFile;
import java.io.Reader;
import java.net.InetAddress;
import java.net.UnknownHostException;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.Objects;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;

/**
 * <p>Android API for GravityCone — Minecraft LAN tunneling over P2P.</p>
 *
 * <p>Design follows the same state-machine pattern as Terracotta
 * ({@code TerracottaAndroidAPI}). The caller initializes the engine,
 * triggers state transitions ({@link #setScanning} / {@link #setGuesting}),
 * and polls {@link #getState()} to monitor progress.</p>
 *
 * <h1>State Machine</h1>
 * <pre>
 *   idle ──→ host-scanning ──→ host-starting ──→ host-ready   (create room)
 *    │
 *    └──→ guest-connecting ──→ guest-ready                    (join room)
 *                    ↓
 *                 error
 * </pre>
 *
 * <h1>State JSON Formats</h1>
 *
 * <h2>idle</h2>
 * <pre>{@code {"state":"waiting","index":N}}</pre>
 *
 * <h2>host-scanning</h2>
 * <pre>{@code {"state":"host-scanning","index":N}}</pre>
 *
 * <h2>host-starting</h2>
 * <pre>{@code {"state":"host-starting","index":N,"room":"U/..."}}</pre>
 *
 * <h2>host-ready (Java Edition / Scaffolding)</h2>
 * <pre>{@code
 * {"state":"host-ok","index":N,"protocol":"scaffolding",
 *  "room":"U/1234-5678-9012-3456","mc_port":25565}
 * }</pre>
 *
 * <h2>host-ready (Bedrock Edition / PaperConnect)</h2>
 * <pre>{@code
 * {"state":"host-ok","index":N,"protocol":"paperconnect",
 *  "sub_protocol":"nethernet","room":"P/...","game_port":45678}
 * }</pre>
 *
 * <h2>guest-connecting</h2>
 * <pre>{@code {"state":"guest-connecting","index":N,"room":"U/...","step":"connecting"}}</pre>
 *
 * <h2>guest-ready (Java Edition)</h2>
 * <pre>{@code
 * {"state":"guest-ok","index":N,"protocol":"scaffolding",
 *  "url":"127.0.0.1:25565"}
 * }</pre>
 *
 * <h2>guest-ready (Bedrock Edition)</h2>
 * <pre>{@code
 * {"state":"guest-ok","index":N,"protocol":"paperconnect",
 *  "sub_protocol":"nethernet","url":"127.0.0.1:45678"}
 * }</pre>
 *
 * <h2>error</h2>
 * <pre>{@code {"state":"exception","index":N,"type":0}}</pre>
 *
 * <h1>VpnService</h1>
 * <p>GravityCone currently operates in no-tun mode (port-forward only),
 * so the {@link VpnServiceCallback} may never be triggered. It exists for
 * future TUN-mode support. When TUN mode is needed, register a callback
 * via {@link #initialize} and handle requests with
 * {@link #getPendingVpnServiceRequest()}.</p>
 *
 * <h1>Thread Safety</h1>
 * <p>All public methods are thread-safe and may be called from any thread.
 * {@link #getState()} is designed for periodic polling (e.g. every 500ms).</p>
 *
 * <h1>Lifecycle</h1>
 * <pre>
 *   initialize(context, callback)
 *     → setScanning / setGuesting / stunProbe
 *       → getState() (poll)
 *         → setWaiting()
 *           → shutdown()
 * </pre>
 */
public final class GravityConeAndroidAPI {
    private static final String TAG = "GravityConeAPI";

    // =====================================================================
    // VpnService interfaces
    // =====================================================================

    /**
     * Callback invoked when EasyTier requests a TUN device via VpnService.
     * The host app must call {@link #getPendingVpnServiceRequest()} to
     * retrieve the request and either fulfill or reject it within 30 seconds.
     */
    public interface VpnServiceCallback {
        void onStartVpnService();
    }

    /**
     * A pending VpnService request from EasyTier.
     * The host app must call {@link #startVpnService} or {@link #reject()}
     * within 30 seconds, or the request times out.
     */
    public interface VpnServiceRequest {
        /**
         * Create the VPN connection and fulfill the request.
         *
         * @param builder A pre-configured VpnService.Builder. The host may
         *                further customize the builder before passing it in.
         * @return The established TUN file descriptor. The host must close
         *         this descriptor when EasyTier is stopped.
         * @throws RuntimeException if {@link VpnService.Builder#establish()}
         *         returns null.
         */
        ParcelFileDescriptor startVpnService(VpnService.Builder builder);

        /** Reject the VpnService request. */
        void reject();
    }

    // =====================================================================
    // Metadata
    // =====================================================================

    /** Version and build metadata for GravityCone. */
    public static final class Metadata {
        private final String gravityconeVersion;
        private final long compileTime;
        private final String easyTierVersion;

        private Metadata(String gravityconeVersion, long compileTime, String easyTierVersion) {
            this.gravityconeVersion = gravityconeVersion;
            this.compileTime = compileTime;
            this.easyTierVersion = easyTierVersion;
        }

        /** @return GravityCone version string (e.g. "0.1.3-alpha"). */
        public String getGravityconeVersion() { return gravityconeVersion; }

        /** @return Build timestamp (same format as {@link System#currentTimeMillis()}). */
        public long getCompileTime() { return compileTime; }

        /** @return EasyTier version string (e.g. "v2.6.4"). */
        public String getEasyTierVersion() { return easyTierVersion; }
    }

    // =====================================================================
    // RoomType
    // =====================================================================

    /** Room code types recognized by GravityCone. */
    public enum RoomType {
        /** ScaffoldingMC protocol (Minecraft Java Edition), "U/" prefix.
         *  Value 3 is compatible with Terracotta's SCAFFOLDING. */
        SCAFFOLDING,
        /** PaperConnect protocol (Minecraft Bedrock Edition), "P/" prefix. */
        PAPER_CONNECT
    }

    // =====================================================================
    // Native library loading
    // =====================================================================

    static {
        // libeasytier_ffi must be loaded first (it's a dependency of libgravitycone).
        try {
            System.loadLibrary("easytier_ffi");
        } catch (UnsatisfiedLinkError e) {
            Log.w(TAG, "libeasytier_ffi not found, assuming static link", e);
        }
        System.loadLibrary("gravitycone");
    }

    // =====================================================================
    // Global state
    // =====================================================================

    private static volatile VpnServiceRequest pendingRequest = null;
    private static final Object vpnFdLock = new Object();
    private static ParcelFileDescriptor vpnFd = null;
    private static volatile RuntimeContext runtimeContext = null;

    private static final class RuntimeContext {
        final VpnServiceCallback vpnServiceCallback;
        final RandomAccessFile logging;
        final File logFile;

        RuntimeContext(VpnServiceCallback vpnServiceCallback, RandomAccessFile logging, File logFile) {
            this.vpnServiceCallback = vpnServiceCallback;
            this.logging = logging;
            this.logFile = logFile;
        }
    }

    // =====================================================================
    // Initialization & Shutdown
    // =====================================================================

    /**
     * Initialize the GravityCone engine.
     *
     * <p>Must be called once before any other method. Creates the engine
     * working directory under {@code context.getFilesDir()}, writes logs to
     * the app-specific external files directory when available, initializes
     * the Go runtime, and starts EasyTier in-process via libeasytier_ffi.</p>
     *
     * @param context  An Android context (used for files dir).
     * @param callback Optional VpnService callback for TUN mode. Pass null
     *                 for the default no-tun (port-forward) mode.
     * @return Metadata with version information.
     * @throws RuntimeException if initialization fails.
     */
    public static synchronized Metadata initialize(Context context,
                                                    @Nullable VpnServiceCallback callback) {
        Objects.requireNonNull(context, "context");

        if (runtimeContext != null) {
            throw new IllegalStateException("GravityCone has already been initialized.");
        }

        File root = new File(context.getFilesDir(), "net.gravitycone.ffi");
        File base = new File(root, "rs");
        if (!base.mkdirs() && !base.isDirectory()) {
            throw new RuntimeException("Cannot create net.gravitycone.ffi/rs directory.");
        }

        File externalFiles = context.getExternalFilesDir(null);
        File logDir = externalFiles != null
            ? new File(externalFiles, "gravitycone")
            : root;
        if (!logDir.mkdirs() && !logDir.isDirectory()) {
            throw new RuntimeException("Cannot create GravityCone log directory: " + logDir);
        }
        File logFile = new File(logDir, "application.log");

        RandomAccessFile logging;
        int fd;
        try {
            logging = new RandomAccessFile(logFile, "rw");
            fd = ParcelFileDescriptor.dup(logging.getFD()).detachFd();
        } catch (IOException e) {
            throw new RuntimeException(e);
        }
        Log.i(TAG, "GravityCone log file: " + logFile.getAbsolutePath());

        VpnServiceCallback cb = callback != null ? callback : () -> {
            Log.w(TAG, "VpnService requested but no callback registered.");
        };

        int code = nativeInit(base.getAbsolutePath(), fd);
        if (code != 0) {
            throw new RuntimeException("Cannot start GravityCone: error code " + code);
        }

        runtimeContext = new RuntimeContext(cb, logging, logFile);

        // Parse metadata JSON from native.
        String metaStr = nativeGetMetadata();
        if (metaStr == null) {
            throw new AssertionError("nativeGetMetadata returned null.");
        }

        // Simple JSON parsing without a full JSON library dependency.
        // Format: {"version":"...","compile_time":...,"easytier_version":"..."}
        String version = extractJsonString(metaStr, "version");
        long compileTime = extractJsonLong(metaStr, "compile_time");
        String etVersion = extractJsonString(metaStr, "easytier_version");

        return new Metadata(
            version != null ? version : "unknown",
            compileTime,
            etVersion != null ? etVersion : "unknown"
        );
    }

    /**
     * Shut down the GravityCone engine.
     * Stops all active rooms/connections and releases native resources.
     * Safe to call even if not initialized.
     */
    public static synchronized void shutdown() {
        if (runtimeContext == null) return;
        nativeShutdown();
        closeVpnFd();
        runtimeContext = null;
    }

    // =====================================================================
    // State polling
    // =====================================================================

    /**
     * Get the current state as a JSON string.
     *
     * <p>Call this periodically (e.g. every 500ms) to track state changes.
     * Each state includes an {@code "index"} field that increments on every
     * transition — compare indices to detect changes without parsing JSON.</p>
     *
     * @return JSON state string. Never null after initialization.
     * @throws IllegalStateException if the engine hasn't been initialized.
     */
    public static String getState() {
        assertStarted();
        return nativeGetState();
    }

    // =====================================================================
    // State transitions
    // =====================================================================

    /**
     * Transition to idle/waiting state.
     * Stops any active room or connection.
     */
    public static void setWaiting() {
        assertStarted();
        nativeSetWaiting();
        closeVpnFd();
    }

    /**
     * Start scanning for a local Minecraft server and create a room.
     *
     * <p>State transitions: idle → host-scanning → host-starting → host-ready.
     * Poll {@link #getState()} to track progress.</p>
     *
     * @param room     Optional room code (null or empty = auto-generate).
     *                 Only used for re-creating a specific room.
     * @param player   Player name. Null or empty uses default "Player".
     * @param protocol Protocol: "scaffolding" (Java Edition, default),
     *                 "paperconnect" (Bedrock Edition). Null = default.
     */
    public static void setScanning(@Nullable String room,
                                   @Nullable String player,
                                   @Nullable String protocol) {
        assertStarted();
        nativeSetScanning(
            room != null ? room : "",
            player != null ? player : "",
            protocol != null ? protocol : ""
        );
    }

    /**
     * Join a remote room.
     *
     * <p>State transitions: idle → guest-connecting → guest-ready.
     * Poll {@link #getState()} to track progress.</p>
     *
     * @param room   Room code (required). Supports U/ and P/ prefixes.
     * @param player Player name. Null or empty uses default "Player".
     * @return true if the room code is valid and the connection started.
     */
    public static boolean setGuesting(String room, @Nullable String player) {
        Objects.requireNonNull(room, "room");
        assertStarted();
        return nativeSetGuesting(
            room,
            player != null ? player : ""
        );
    }

    // =====================================================================
    // Utilities
    // =====================================================================

    /**
     * Run a STUN NAT type probe.
     *
     * <p>This is a blocking call that takes 3-10 seconds.</p>
     *
     * @return JSON string with NAT type info, or an error object.
     *         Format on success: {@code {"udp_nat_type":3,"tcp_nat_type":4,...}}
     *         Format on error: {@code {"error":"stun probe failed: ..."}}
     */
    public static String stunProbe() {
        assertStarted();
        return nativeStunProbe();
    }

    /**
     * Check the type of a room code without connecting.
     *
     * @param code Room code string.
     * @return The room type, or null if invalid.
     */
    @Nullable
    public static RoomType parseRoomCode(String code) {
        Objects.requireNonNull(code, "code");
        assertStarted();
        switch (nativeVerifyRoomCode(code)) {
            case -1: return null;
            case 3:  return RoomType.SCAFFOLDING;
            case 4:  return RoomType.PAPER_CONNECT;
            default: return null;
        }
    }

    /**
     * Get version and build metadata.
     *
     * @return JSON string: {@code {"version":"...","compile_time":...,"easytier_version":"..."}}
     */
    public static String getMetadata() {
        assertStarted();
        return nativeGetMetadata();
    }

    /**
     * Release the VPN descriptor after the native engine has stopped its
     * EasyTier instance. This is idempotent and must not be called between
     * PaperConnect's discovery and static-forward phases.
     */
    public static void closeVpnFd() {
        ParcelFileDescriptor connection;
        synchronized (vpnFdLock) {
            connection = vpnFd;
            vpnFd = null;
        }
        if (connection == null) {
            return;
        }
        int fd = connection.getFd();
        try {
            connection.close();
            Log.i(TAG, "VPN descriptor closed: fd=" + fd);
        } catch (IOException e) {
            Log.w(TAG, "Failed to close VPN descriptor: fd=" + fd, e);
        }
    }

    /**
     * Get the current pending VpnService request.
     *
     * <p>Called from the {@link VpnServiceCallback} to retrieve the request
     * details. Must call either {@link VpnServiceRequest#startVpnService} or
     * {@link VpnServiceRequest#reject()} within 30 seconds.</p>
     *
     * @return The pending request.
     * @throws IllegalStateException if no pending request exists.
     */
    public static VpnServiceRequest getPendingVpnServiceRequest() {
        VpnServiceRequest req = pendingRequest;
        if (req == null) {
            throw new IllegalStateException("There's no pending VpnService request.");
        }
        return req;
    }

    // =====================================================================
    // Logging
    // =====================================================================

    /**
     * Return the engine log file. On normal Android devices this is:
     * {@code /storage/emulated/0/Android/data/<package>/files/gravitycone/application.log}.
     *
     * <p>The path falls back to the app's internal files directory when
     * external app storage is unavailable.</p>
     */
    public static File getLogFile() {
        assertStarted();
        return runtimeContext.logFile;
    }

    /**
     * Collect logs of the GravityCone engine.
     *
     * <p>Callers must immediately copy all data out of the returned reader
     * and close it. While the reader is open, all state-transition methods
     * may block. {@link #parseRoomCode} and {@link #getMetadata} are safe
     * to call concurrently.</p>
     *
     * @return A reader containing the application log.
     */
    public static Reader collectLogs() throws IOException {
        assertStarted();

        RandomAccessFile file = runtimeContext.logging;
        file.seek(0);

        return new BufferedReader(new InputStreamReader(new InputStream() {
            private final AtomicBoolean closed = new AtomicBoolean(false);

            @Override
            public int read() throws IOException {
                assertOpen();
                return file.read();
            }

            @Override
            public int read(byte[] b) throws IOException {
                assertOpen();
                return file.read(b);
            }

            @Override
            public int read(byte[] b, int off, int len) throws IOException {
                assertOpen();
                return file.read(b, off, len);
            }

            @Override
            public int available() throws IOException {
                assertOpen();
                return Math.toIntExact(file.length() - file.getFilePointer());
            }

            @Override
            public void close() throws IOException {
                super.close();
                closed.set(true);
            }

            private void assertOpen() throws IOException {
                if (closed.get()) {
                    throw new IOException("Stream has already been closed");
                }
            }
        }, StandardCharsets.UTF_8));
    }

    // =====================================================================
    // VpnService callback (called from native thread)
    // =====================================================================

    private static final long FD_PENDING = ((long) Integer.MAX_VALUE) + 1;
    private static final long FD_REJECT = FD_PENDING + 1;

    @SuppressWarnings("unused") // Native callback via JNI
    private static int onVpnServiceStateChanged(
            byte ip1, byte ip2, byte ip3, byte ip4,
            short networkLength, String cidr) throws UnknownHostException {

        if (pendingRequest != null) {
            throw new AssertionError("VpnService request already pending.");
        }

        AtomicLong fd = new AtomicLong(FD_PENDING);
        InetAddress address = InetAddress.getByAddress(
            new byte[]{ip1, ip2, ip3, ip4});

        pendingRequest = new VpnServiceRequest() {
            @Override
            public ParcelFileDescriptor startVpnService(VpnService.Builder builder) {
                ParcelFileDescriptor previousConnection;
                synchronized (vpnFdLock) {
                    previousConnection = vpnFd;
                    vpnFd = null;
                }
                if (previousConnection != null) {
                    // PaperConnect restarts EasyTier after discovery so it can
                    // install static forwards. The descriptor belongs to the
                    // stopped phase-one instance and cannot be injected into
                    // phase two again.
                    try {
                        previousConnection.close();
                        Log.i(TAG, "Closed previous EasyTier VPN descriptor before restart");
                    } catch (IOException e) {
                        Log.w(TAG, "Failed to close previous EasyTier VPN descriptor", e);
                    }
                }

                builder.addAddress(address, networkLength)
                       .addDnsServer("223.5.5.5")
                       .addDnsServer("114.114.114.114")
                       // 关键：不调用 allowBypass() 时 Android 会让 VPN 成为默认网络，
                       // 本机所有应用（包括 Minecraft）的流量（广播/连接）全进 TUN 被
                       // EasyTier 丢弃，导致本机 fake server 收不到发现请求。
                       // allowBypass() 后只有 addRoute 的虚拟网段（10.144.144.0/24）
                       // 走 VPN，Minecraft 的局域网广播/连接走物理网络（WiFi），
                       // EasyTier 的公网 peer 连接也不会被自己的 TUN 吞掉。
                       .allowBypass();

                if (cidr != null && !cidr.isEmpty()) {
                    for (String part : cidr.split("\0")) {
                        String[] parts = part.split("/", 3);
                        if (parts.length != 2) {
                            throw new IllegalArgumentException(
                                "Illegal CIDR: " + Arrays.toString(parts));
                        }
                        builder.addRoute(parts[0], Integer.parseInt(parts[1]));
                    }
                }

                ParcelFileDescriptor connection = builder.establish();
                if (connection == null) {
                    throw new RuntimeException("Cannot establish VPN connection.");
                }

                synchronized (vpnFdLock) {
                    vpnFd = connection;
                }
                fd.set((long) connection.getFd());
                Log.i(TAG, "VPN established: address=" + address.getHostAddress()
                        + "/" + networkLength + ", cidr=" + cidr
                        + ", fd=" + connection.getFd());
                return connection;
            }

            @Override
            public void reject() {
                fd.set(FD_REJECT);
            }
        };

        runtimeContext.vpnServiceCallback.onStartVpnService();

        long timestamp = System.currentTimeMillis();
        while (true) {
            long value = fd.get();
            if (value == FD_PENDING) {
                if (System.currentTimeMillis() - timestamp >= 30000) {
                    Log.wtf(TAG, "VpnService request not fulfilled within 30s.");
                    pendingRequest = null;
                    throw new IllegalStateException("VpnService request timeout");
                }
                Thread.yield();
            } else if (value == FD_REJECT) {
                pendingRequest = null;
                throw new IllegalStateException("VpnService request rejected");
            } else {
                pendingRequest = null;
                if ((int) value != value) {
                    throw new AssertionError("File descriptor too large.");
                }
                return (int) value;
            }
        }
    }

    // =====================================================================
    // Internal helpers
    // =====================================================================

    private static void assertStarted() {
        if (runtimeContext == null) {
            throw new IllegalStateException(
                "GravityCone hasn't been initialized. Call initialize() first.");
        }
    }

    // Minimal JSON value extraction (avoids dependency on a full JSON library).
    private static String extractJsonString(String json, String key) {
        String search = "\"" + key + "\":\"";
        int start = json.indexOf(search);
        if (start < 0) return null;
        start += search.length();
        int end = json.indexOf('"', start);
        if (end < 0) return null;
        return json.substring(start, end);
    }

    private static long extractJsonLong(String json, String key) {
        String search = "\"" + key + "\":";
        int start = json.indexOf(search);
        if (start < 0) return 0;
        start += search.length();
        int end = start;
        while (end < json.length() &&
               (Character.isDigit(json.charAt(end)) || json.charAt(end) == '-')) {
            end++;
        }
        if (end == start) return 0;
        try {
            return Long.parseLong(json.substring(start, end));
        } catch (NumberFormatException e) {
            return 0;
        }
    }

    // =====================================================================
    // Native methods
    // =====================================================================

    private static native int nativeInit(String baseDir, int loggingFd);
    private static native String nativeGetState();
    private static native void nativeSetWaiting();
    private static native void nativeSetScanning(String room, String player, String protocol);
    private static native boolean nativeSetGuesting(String room, String player);
    private static native int nativeVerifyRoomCode(String code);
    private static native String nativeStunProbe();
    private static native String nativeGetMetadata();
    private static native void nativeShutdown();
    private static native int nativeSetTunFd(String instanceName, int fd);
}
