package com.gravitycone.test;

import android.net.VpnService;
import android.os.ParcelFileDescriptor;

import androidx.annotation.NonNull;

import net.gravitycone.ffi.GravityConeAndroidAPI;

/**
 * VpnService，用于为 EasyTier 建立 TUN 接口。
 *
 * <p>{@link android.net.VpnService.Builder} 是 VpnService 的<b>非静态内部类</b>，
 * 只能通过 VpnService 实例创建（{@code svc.new Builder()} 或在子类实例方法中
 * {@code new Builder()}），因此本 Service 必须真正启动运行（MainActivity
 * 在 onCreate 中 {@code startService}），持有 attach 的实例供回调使用。</p>
 *
 * <p>本应用不实际读写 TUN 数据包（GravityCone 为 no_tun 端口转发模式，
 * fd 交给引擎处理），Service 实例仅用于创建 Builder 并建立 VPN 接口。</p>
 */
public class GravityConeVpnService extends VpnService {

    /** 运行中的实例（由 onCreate/onDestroy 维护）。 */
    private static volatile GravityConeVpnService sInstance;

    @Override
    public void onCreate() {
        super.onCreate();
        sInstance = this;
    }

    @Override
    public void onDestroy() {
        if (sInstance == this) {
            sInstance = null;
        }
        super.onDestroy();
    }

    /**
     * 等待 Service 实例就绪（startService 是异步的，onCreate 尚未执行时
     * 实例为 null）。EasyTier 的 TUN 请求发生在引擎初始化之后，此时实例
     * 必然已就绪，此等待仅作兜底。
     *
     * @param timeoutMs 最长等待时间（毫秒）
     * @throws InterruptedException 等待被中断
     * @throws IllegalStateException 超时后仍未就绪
     */
    @NonNull
    public static GravityConeVpnService awaitInstance(long timeoutMs) throws InterruptedException {
        long deadline = System.currentTimeMillis() + timeoutMs;
        GravityConeVpnService svc = sInstance;
        while (svc == null && System.currentTimeMillis() < deadline) {
            Thread.sleep(50);
            svc = sInstance;
        }
        if (svc == null) {
            throw new IllegalStateException("GravityConeVpnService 未启动");
        }
        return svc;
    }

    /**
     * 建立 VPN 接口并交给 GravityCone 引擎。
     *
     * <p>地址/路由/DNS 由 {@code GravityConeAndroidAPI} 内部配置，此处仅提供
     * Builder 并调用 {@link GravityConeAndroidAPI.VpnServiceRequest#startVpnService}。</p>
     *
     * @param request 待履行的 VpnService 请求
     * @return 建立的 TUN 文件描述符（调用方必须持有引用，防止被 GC 关闭）
     */
    @NonNull
    public ParcelFileDescriptor establishVpn(GravityConeAndroidAPI.VpnServiceRequest request) {
        return request.startVpnService(new Builder());
    }
}
