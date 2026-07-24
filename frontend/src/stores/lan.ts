import { defineStore } from 'pinia'
import { Events } from '@wailsio/runtime'
import { StartDiscovery, StopDiscovery, GetDiscoveredServers } from '@/../bindings/gravitycone/core/minecraft/lanservice'
import type { LanServer } from '@/../bindings/gravitycone/core/minecraft/models'

type EventUnsubscriber = () => void

export const useLanStore = defineStore('lan', {
  state: () => ({
    servers: [] as LanServer[],
    discovering: false,
    error: '',
    _unsubscribers: [] as EventUnsubscriber[],
  }),
  actions: {
    async startDiscovery() {
      this.discovering = true
      this.error = ''
      try {
        this.startEvents()
        await StartDiscovery()
      } catch (e: any) {
        this.error = e?.message || String(e)
        this.discovering = false
      }
    },
    async stopDiscovery() {
      this.stopEvents()
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
    startEvents() {
      this.stopEvents()

      const unsub1 = Events.On('lan.server_found', (event: any) => {
        const server = event.data
        const idx = this.servers.findIndex(s => s.ip === server.ip && s.port === server.port)
        if (idx >= 0) {
          this.servers[idx] = server
        } else {
          this.servers.push(server)
        }
      }) as unknown as EventUnsubscriber

      const unsub2 = Events.On('lan.server_lost', (event: any) => {
        const data = event.data
        this.servers = this.servers.filter(s => !(s.ip === data.ip && s.port === data.port))
      }) as unknown as EventUnsubscriber

      this._unsubscribers = [unsub1, unsub2]
    },
    stopEvents() {
      for (const unsub of this._unsubscribers) {
        try { unsub() } catch {}
      }
      this._unsubscribers = []
    },
  },
})
