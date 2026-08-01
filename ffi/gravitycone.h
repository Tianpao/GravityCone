/*
 * GravityCone FFI — Platform-Agnostic C API
 * ==========================================
 *
 * This header defines the C-level API for GravityCone, following the same
 * state-machine design as Terracotta. Consumers (Android via JNI, iOS via
 * C bindings, desktop via CGo/FFI) call these functions to manage Minecraft
 * LAN tunneling rooms.
 *
 * ## State Machine
 *
 *     Idle ──→ HostScanning ──→ HostStarting ──→ HostReady (host)
 *      │
 *      └──→ GuestConnecting ──→ GuestReady (guest)
 *                     ↓
 *                Error
 *
 * ## State JSON Formats
 *
 * ### idle
 *     {"state":"waiting","index":N}
 *
 * ### host-scanning
 *     {"state":"host-scanning","index":N}
 *
 * ### host-starting
 *     {"state":"host-starting","index":N,"room":"U/..."}
 *
 * ### host-ready (Java Edition / Scaffolding)
 *     {"state":"host-ok","index":N,"protocol":"scaffolding",
 *      "room":"U/1234-5678-9012-3456","mc_port":25565}
 *
 * ### host-ready (Bedrock Edition / PaperConnect)
 *     {"state":"host-ok","index":N,"protocol":"paperconnect",
 *      "sub_protocol":"nethernet","room":"P/...","game_port":45678}
 *
 * ### guest-connecting
 *     {"state":"guest-connecting","index":N,"room":"U/...","step":"connecting"}
 *
 * ### guest-ready (Java Edition)
 *     {"state":"guest-ok","index":N,"protocol":"scaffolding",
 *      "url":"127.0.0.1:25565"}
 *
 * ### guest-ready (Bedrock Edition)
 *     {"state":"guest-ok","index":N,"protocol":"paperconnect",
 *      "sub_protocol":"nethernet","url":"127.0.0.1:45678"}
 *
 * ### error
 *     {"state":"exception","index":N,"type":0}
 *
 * ## Room Code Types
 *
 * | Return | Type                     | Prefix |
 * |--------|--------------------------|--------|
 * | -1     | Invalid                  |        |
 * | 3      | Scaffolding (Java)       | U/     |
 * | 4      | PaperConnect (Bedrock)   | P/     |
 *
 * ## Thread Safety
 *
 * All functions are thread-safe and may be called from any thread.
 *
 * ## Memory Management
 *
 * Strings returned by gc_get_state() and gc_get_metadata() are allocated
 * by the library and MUST be freed by the caller using gc_free_string().
 *
 * ## Integration with Android
 *
 * For Android integration, see Terracotta's TerracottaAndroidAPI.java for
 * the recommended JNI wrapper pattern. The JNI native methods should call
 * the corresponding gc_* functions.
 */

#ifndef GRAVITYCONE_FFI_H
#define GRAVITYCONE_FFI_H

#ifdef __cplusplus
extern "C" {
#endif

/**
 * Initialize the GravityCone engine.
 *
 * @param base_dir  Writable directory for logs, machine-id, and config.
 *                  Must already exist. Pass NULL for default (current dir).
 * @return 0 on success, non-zero on failure.
 */
int gc_init(const char *base_dir);

/**
 * Shut down the GravityCone engine.
 *
 * Stops all active rooms and connections, frees internal resources.
 * After calling this, gc_init() may be called again to re-initialize.
 */
void gc_shutdown(void);

/**
 * Get the current state as a JSON string.
 *
 * The returned string must be freed with gc_free_string().
 * Returns a snapshot that reflects the state at the time of the call;
 * callers should poll this periodically to detect state changes.
 *
 * @return JSON string (never NULL after gc_init).
 */
char *gc_get_state(void);

/**
 * Transition to the idle/waiting state.
 *
 * If a room is currently active (host or guest), it will be stopped
 * before transitioning to idle. Safe to call from any state.
 */
void gc_set_waiting(void);

/**
 * Start scanning for a local Minecraft server and create a room.
 *
 * Transitions through: idle → host-scanning → host-starting → host-ready
 * Poll gc_get_state() to track progress.
 *
 * @param room      Optional room code. Pass NULL or "" to auto-generate.
 * @param player    Player name. Pass NULL for default "Player".
 * @param protocol  Protocol to use:
 *                  NULL or "" = ScaffoldingMC (Java Edition)
 *                  "scaffolding" = ScaffoldingMC (Java Edition)
 *                  "paperconnect" = PaperConnect (Bedrock Edition)
 */
void gc_set_scanning(const char *room, const char *player, const char *protocol);

/**
 * Join a remote room.
 *
 * Transitions through: idle → guest-connecting → guest-ready
 * Poll gc_get_state() to track progress.
 *
 * @param room    Room code (required). Supports U/ (Scaffolding) and P/ (PaperConnect) prefixes.
 * @param player  Player name. Pass NULL for default "Player".
 * @return 1 if the room code is valid and connection started, 0 otherwise.
 */
int gc_set_guesting(const char *room, const char *player);

/**
 * Run a STUN NAT type probe.
 *
 * This is a blocking call that takes 3-10 seconds.
 *
 * Desktop: probes via easytier-cli stun.
 * FFI mode (Android): reads the NAT type the running EasyTier instance
 * collects internally; the instance must be running (create/join a room
 * first) and the probe may not have finished yet, in which case an error
 * JSON is returned.
 *
 * On success, returns JSON:
 *   {"udp_nat_type":1,"tcp_nat_type":2,"last_update_time":1720246800,
 *    "public_ip":["203.0.113.1"],"min_port":30000,"max_port":40000}
 *
 * NAT type values (EasyTier proto NatType, identical on desktop & Android):
 *   0 = Unknown
 *   1 = OpenInternet (Open Internet)
 *   2 = NoPAT
 *   3 = FullCone
 *   4 = Restricted
 *   5 = PortRestricted (Port Restricted Cone)
 *   6 = Symmetric
 *   7 = SymUdpFirewall
 *   8 = SymmetricEasyInc
 *   9 = SymmetricEasyDec
 *
 * On failure, returns JSON: {"error":"stun probe failed: ..."}
 *
 * The returned string must be freed with gc_free_string().
 *
 * @return JSON string (never NULL after gc_init).
 */
char *gc_stun_probe(void);

/**
 * Attach a TUN file descriptor to a network instance.
 *
 * Used on Android to inject the VpnService TUN fd into EasyTier.
 * Even in no_tun mode, EasyTier's mobile build (tun_mobile.rs) expects
 * a TUN fd to be injected via set_tun_fd(). This is called internally
 * after the VpnService callback returns; direct calls are also supported.
 *
 * On platforms that use native TUN devices (Linux, macOS), this is
 * typically not needed.
 *
 * @param inst_name  Instance name (from collect_network_infos).
 * @param fd         TUN file descriptor from VpnService.establish().
 * @return 0 on success, non-zero on failure.
 */
int gc_set_tun_fd(const char *inst_name, int fd);

/**
 * Check the type of a room code without connecting.
 *
 * @param code  Room code string.
 * @return -1 (invalid), 3 (Scaffolding/Java), or 4 (PaperConnect/Bedrock).
 */
int gc_verify_room_code(const char *code);

/**
 * Get version metadata as a JSON string.
 *
 * The returned string must be freed with gc_free_string().
 *
 * @return JSON: {"version":"x.y.z","compile_time":1720246800000,"easytier_version":"v2.6.4"}
 */
char *gc_get_metadata(void);

/**
 * Free a string returned by gc_get_state() or gc_get_metadata().
 *
 * @param s  The string to free.
 */
void gc_free_string(char *s);

/**
 * Get the FFI ABI version.
 *
 * Increments when the API changes incompatibly.
 * Currently returns 1.
 *
 * @return ABI version number.
 */
int gc_version(void);

#ifdef __cplusplus
}
#endif

#endif /* GRAVITYCONE_FFI_H */
