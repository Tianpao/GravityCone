import { defineStore } from 'pinia'
import { Events } from '@wailsio/runtime'
import { CreateRoom, StopRoom, GetRoomStatus, JoinRoom, LeaveRoom, GetConnectionStatus, CancelJoin } from '@/../bindings/gravitycone/core/protocol/scaffolding/scaffoldingservice'
import type { RoomStatus, ConnectionStatus } from '@/../bindings/gravitycone/core/protocol/scaffolding/models'

type EventUnsubscriber = () => void

export const useScaffoldingStore = defineStore('scaffolding', {
  state: () => ({
    // HOST
    roomStatus: null as RoomStatus | null,
    creating: false,
    hostError: '',
    // GUEST
    connectionStatus: null as ConnectionStatus | null,
    joining: false,
    guestError: '',
    // Event listeners
    _hostUnsubscribers: [] as EventUnsubscriber[],
    _guestUnsubscribers: [] as EventUnsubscriber[],
  }),

  getters: {
    isHosting: (state): boolean => state.roomStatus?.running ?? false,
    isConnected: (state): boolean => state.connectionStatus?.connected ?? false,
    hostRoomCode: (state): string => state.roomStatus?.code ?? '',
  },

  actions: {
    async createRoom(mcPort: number, playerName: string) {
      this.creating = true
      this.hostError = ''
      try {
        const result = await CreateRoom(mcPort, playerName, 'GravityCone v1.0.0', '')
        this.roomStatus = result
        this.startHostEvents()
      } catch (e: any) {
        this.hostError = e?.message || String(e)
        throw e
      } finally {
        this.creating = false
      }
    },

    async stopRoom() {
      this.stopHostEvents()
      try {
        await StopRoom()
      } catch {}
      this.roomStatus = null
      this.hostError = ''
    },

    async refreshRoomStatus() {
      try {
        const result = await GetRoomStatus()
        if (result) this.roomStatus = result
      } catch (e: any) {
        this.hostError = e?.message || ''
        this.roomStatus = null
      }
    },

    startHostEvents() {
      this.stopHostEvents()

      // Wails sends raw event data (not WailsEvent wrapper) at runtime
      const unsub1 = Events.On('room.player_joined', (player: any) => {
        if (!this.roomStatus) return
        const players = this.roomStatus.players ?? []
        if (!players.find(p => p.machine_id === player.machine_id)) {
          this.roomStatus = {
            ...this.roomStatus,
            players: [...players, player],
            online_count: players.length + 1,
          }
        }
      }) as unknown as EventUnsubscriber

      const unsub2 = Events.On('room.player_left', (player: any) => {
        if (!this.roomStatus) return
        const players = this.roomStatus.players ?? []
        const filtered = players.filter(p => p.machine_id !== player.machine_id)
        this.roomStatus = {
          ...this.roomStatus,
          players: filtered,
          online_count: filtered.length,
        }
      }) as unknown as EventUnsubscriber

      const unsub3 = Events.On('room.closed', (data: any) => {
        this.hostError = data.reason
        this.roomStatus = null
        this.stopHostEvents()
      }) as unknown as EventUnsubscriber

      this._hostUnsubscribers = [unsub1, unsub2, unsub3]
    },

    stopHostEvents() {
      for (const unsub of this._hostUnsubscribers) {
        try { unsub() } catch {}
      }
      this._hostUnsubscribers = []
    },

    async cancelJoin() {
      this.stopGuestEvents()
      try {
        await CancelJoin()
      } catch {}
    },

    async joinRoom(roomCode: string, playerName: string) {
      this.joining = true
      this.guestError = ''
      try {
        const result = await JoinRoom(roomCode, playerName, 'GravityCone v1.0.0', '')
        this.connectionStatus = result
        this.startGuestEvents()
      } catch (e: any) {
        this.guestError = e?.message || String(e)
        throw e
      } finally {
        this.joining = false
      }
    },

    async leaveRoom() {
      this.stopGuestEvents()
      try {
        await LeaveRoom()
      } catch {}
      this.connectionStatus = null
      this.guestError = ''
    },

    async refreshConnectionStatus() {
      try {
        const result = await GetConnectionStatus()
        if (result) {
          this.connectionStatus = result
          if (!result.connected && result.disconnect_reason) {
            this.guestError = result.disconnect_reason
          }
        }
      } catch {}
    },

    startGuestEvents() {
      this.stopGuestEvents()

      const unsub = Events.On('room.disconnected', (data: any) => {
        if (this.connectionStatus) {
          this.connectionStatus = {
            ...this.connectionStatus,
            connected: false,
            disconnect_reason: data.reason,
          }
        }
      }) as unknown as EventUnsubscriber

      this._guestUnsubscribers = [unsub]
    },

    stopGuestEvents() {
      for (const unsub of this._guestUnsubscribers) {
        try { unsub() } catch {}
      }
      this._guestUnsubscribers = []
    },

    reset() {
      this.stopHostEvents()
      this.stopGuestEvents()
      this.$reset()
    },
  },
})
