import { defineStore } from 'pinia'
import { CreateRoom, StopRoom, GetRoomStatus, JoinRoom, LeaveRoom, GetConnectionStatus } from '@/../bindings/changeme/core/scaffoldingservice'
import type { RoomStatus, ConnectionStatus } from '@/../bindings/changeme/core/models'

export const useScaffoldingStore = defineStore('scaffolding', {
  state: () => ({
    // HOST
    roomStatus: null as RoomStatus | null,
    creating: false,
    // GUEST
    connectionStatus: null as ConnectionStatus | null,
    joining: false,
    // Shared
    error: '',
  }),

  getters: {
    isHosting: (state): boolean => state.roomStatus?.running ?? false,
    isConnected: (state): boolean => state.connectionStatus?.connected ?? false,
    hostRoomCode: (state): string => state.roomStatus?.code ?? '',
  },

  actions: {
    async createRoom(mcPort: number, playerName: string) {
      this.creating = true
      this.error = ''
      try {
        const result = await CreateRoom(mcPort, playerName)
        this.roomStatus = result
      } catch (e: any) {
        this.error = e?.message || String(e)
        throw e
      } finally {
        this.creating = false
      }
    },

    async stopRoom() {
      try {
        await StopRoom()
      } catch {}
      this.roomStatus = null
      this.error = ''
    },

    async refreshRoomStatus() {
      try {
        const result = await GetRoomStatus()
        if (result) this.roomStatus = result
      } catch {}
    },

    async joinRoom(roomCode: string, playerName: string) {
      this.joining = true
      this.error = ''
      try {
        const result = await JoinRoom(roomCode, playerName)
        this.connectionStatus = result
      } catch (e: any) {
        this.error = e?.message || String(e)
        throw e
      } finally {
        this.joining = false
      }
    },

    async leaveRoom() {
      try {
        await LeaveRoom()
      } catch {}
      this.connectionStatus = null
      this.error = ''
    },

    async refreshConnectionStatus() {
      try {
        const result = await GetConnectionStatus()
        if (result) {
          this.connectionStatus = result
          if (!result.connected && result.disconnect_reason) {
            this.error = result.disconnect_reason
          }
        }
      } catch {}
    },

    reset() {
      this.$reset()
    },
  },
})
