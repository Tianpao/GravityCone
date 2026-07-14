import { defineStore } from 'pinia'
import { SetCustomPeers } from '@/../bindings/gravitycone/core/easytier/settingsservice'

const STORAGE_KEY = 'gravitycone-custom-peers'

export const useSettingsStore = defineStore('settings', {
  state: () => ({
    customPeers: [] as string[],
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
      this.loaded = true
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
