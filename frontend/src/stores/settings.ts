import { defineStore } from 'pinia'
import { SetCustomPeers, SetP2PDisabled } from '@/../bindings/gravitycone/core/easytier/settingsservice'

const STORAGE_KEY = 'gravitycone-custom-peers'
const P2P_DISABLED_KEY = 'gravitycone-p2p-disabled'

export const useSettingsStore = defineStore('settings', {
  state: () => ({
    customPeers: [] as string[],
    p2pDisabled: localStorage.getItem(P2P_DISABLED_KEY) === 'true',
    loaded: false,
  }),

  actions: {
    async loadPeers() {
      const saved = localStorage.getItem(STORAGE_KEY)
      if (saved) {
        try {
          this.customPeers = JSON.parse(saved)
        } catch {
          this.customPeers = []
        }
      }

      // Apply custom peers to Go backend (they get combined with defaults there)
      if (this.customPeers.length > 0) {
        try {
          await SetCustomPeers(this.customPeers)
        } catch {}
      }

      // 恢复持久化的禁止 P2P 状态（启动后创建/加入房间前保持与后端一致）
      await this.applyP2PDisabled()
      this.loaded = true
    },

    // toggleP2PDisabled 切换"禁止P2P"并同步到后端（仅 GUI 生效，CLI 无此入口）。
    async toggleP2PDisabled() {
      this.p2pDisabled = !this.p2pDisabled
      localStorage.setItem(P2P_DISABLED_KEY, String(this.p2pDisabled))
      await this.applyP2PDisabled()
    },

    // applyP2PDisabled 把当前状态同步到后端。
    async applyP2PDisabled() {
      try {
        await SetP2PDisabled(this.p2pDisabled)
      } catch {
        // 后端同步失败不影响联机流程
      }
    },

    async addPeer(peer: string) {
      const trimmed = peer.trim()
      if (!trimmed || this.customPeers.includes(trimmed)) return
      this.customPeers.push(trimmed)
      await this.save()
    },

    async removePeer(peer: string) {
      this.customPeers = this.customPeers.filter(p => p !== peer)
      await this.save()
    },

    async save() {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(this.customPeers))
      try {
        await SetCustomPeers(this.customPeers)
      } catch {}
    },
  },
})
