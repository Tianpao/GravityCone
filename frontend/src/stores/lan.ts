import { defineStore } from 'pinia'
import { StartDiscovery, StopDiscovery, GetDiscoveredServers, CreateRoom } from '@/../bindings/gravitycone/core/minecraft/lanservice'
import type { LanServer } from '@/../bindings/gravitycone/core/minecraft/models'

export const useLanStore = defineStore('lan', {
  state: () => ({
    servers: [] as LanServer[],
    discovering: false,
    error: '',
    creating: false,
  }),
  actions: {
    async startDiscovery() {
      this.discovering = true
      this.error = ''
      try {
        await StartDiscovery()
      } catch (e: any) {
        this.error = e?.message || String(e)
      }
      this.discovering = false
    },
    async stopDiscovery() {
      try {
        await StopDiscovery()
      } catch {
        // ignore
      }
      this.servers = []
      this.discovering = false
      this.error = ''
    },
    async refresh() {
      try {
        const result = await GetDiscoveredServers()
        if (result) {
          this.servers = result
        }
      } catch (e: any) {
        this.error = e?.message || String(e)
      }
    },
    async createRoom(ip: string, port: number) {
      this.creating = true
      this.error = ''
      try {
        await CreateRoom(ip, port)
      } catch (e: any) {
        this.error = e?.message || String(e)
        throw e
      } finally {
        this.creating = false
      }
    },
  },
})
