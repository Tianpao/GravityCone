import { defineStore } from 'pinia'
import { Events } from '@wailsio/runtime'
import {
  CreateRoom, StopRoom, GetRoomStatus,
  JoinRoom, LeaveRoom, GetConnectionStatus, CancelJoin, ConfirmMinecraftEnded
} from '../../bindings/gravitycone/core/protocol/paperconnect/paperconnectservice.js'
import type { PaperConnectRoomStatus, PaperConnectConnectionStatus } from '../../bindings/gravitycone/core/protocol/paperconnect/models.js'

type EventUnsubscriber = () => void
type EventData = Record<string, unknown>

function onPaperConnectEvent(eventName: string, handler: (data: EventData) => void): EventUnsubscriber {
  return Events.On(eventName, (event: any) => handler(event.data ?? {})) as unknown as EventUnsubscriber
}

interface PcState {
  pcRoomStatus: PaperConnectRoomStatus | null
  pcCreating: boolean
  pcHostError: string
  pcConnectionStatus: PaperConnectConnectionStatus | null
  pcJoining: boolean
  pcGuestError: string
  pcPortBusyMessage: string
  _guestUnsubscribers: EventUnsubscriber[]
}

export const usePaperConnectStore = defineStore('paperconnect', {
  state: (): PcState => ({
    // HOST
    pcRoomStatus: null,
    pcCreating: false,
    pcHostError: '',
    // GUEST
    pcConnectionStatus: null,
    pcJoining: false,
    pcGuestError: '',
    pcPortBusyMessage: '',
    _guestUnsubscribers: [],
  }),

  getters: {
    isHostingPc: (state) => state.pcRoomStatus?.running ?? false,
    isConnectedPc: (state) => state.pcConnectionStatus?.connected ?? false,
    hostRoomCodePc: (state) => state.pcRoomStatus?.code ?? '',
  },

  actions: {
    async pcCreateRoom(playerName: string) {
      this.pcCreating = true
      this.pcHostError = ''
      try {
        const result = await CreateRoom(playerName, '')
        this.pcRoomStatus = result
        return result
      } catch (e: any) {
        this.pcHostError = e?.message || String(e)
        throw e
      } finally {
        this.pcCreating = false
      }
    },

    async pcStopRoom() {
      try {
        await StopRoom()
      } catch (e: any) {
        this.pcHostError = e?.message || String(e)
      }
      this.pcRoomStatus = null
    },

    async pcRefreshRoomStatus() {
      try {
        this.pcRoomStatus = await GetRoomStatus()
      } catch (e: any) {
        this.pcHostError = e?.message || String(e)
      }
    },

    async pcJoinRoom(roomCode: string, playerName: string) {
      this.pcJoining = true
      this.pcGuestError = ''
      // Subscribe before JoinRoom starts its asynchronous game-bridge setup so an
      // immediate port_busy event cannot be emitted before the UI is listening.
      this.startGuestEvents()
      try {
        const result = await JoinRoom(roomCode, playerName, '')
        this.pcConnectionStatus = result
        return result
      } catch (e: any) {
        this.stopGuestEvents()
        this.pcGuestError = e?.message || String(e)
        throw e
      } finally {
        this.pcJoining = false
      }
    },

    async pcCancelJoin() {
      this.stopGuestEvents()
      try {
        await CancelJoin()
      } catch { /* ignore */ }
    },

    async pcLeaveRoom() {
      this.stopGuestEvents()
      try {
        await LeaveRoom()
      } catch { /* ignore */ }
      this.pcConnectionStatus = null
    },

    async pcConfirmMinecraftEnded() {
      try {
        await ConfirmMinecraftEnded()
      } catch (e: any) {
        this.pcGuestError = e?.message || String(e)
      }
    },

    startGuestEvents() {
      this.stopGuestEvents()

      const unsubPortBusy = onPaperConnectEvent('paperconnect.connection.port_busy', (data) => {
        this.pcPortBusyMessage = String(data.message || 'Minecraft 正在占用 UDP 端口 7551。请结束 Minecraft 后确认。')
      })
      const unsubReady = onPaperConnectEvent('paperconnect.connection.ready', () => {
        this.pcPortBusyMessage = ''
      })
      const unsubError = onPaperConnectEvent('paperconnect.connection.error', (data) => {
        this.pcGuestError = String(data.message || '游戏连接建立失败，仅控制通道可用')
      })
      const unsubDisconnected = onPaperConnectEvent('paperconnect.room.disconnected', (data) => {
        this.pcPortBusyMessage = ''
        if (this.pcConnectionStatus) {
          this.pcConnectionStatus = {
            ...this.pcConnectionStatus,
            connected: false,
            disconnect_reason: String(data.reason || '连接已断开'),
          }
        }
      })

      this._guestUnsubscribers = [unsubPortBusy, unsubReady, unsubError, unsubDisconnected]
    },

    stopGuestEvents() {
      for (const unsubscribe of this._guestUnsubscribers) {
        try { unsubscribe() } catch { /* ignore */ }
      }
      this._guestUnsubscribers = []
      this.pcPortBusyMessage = ''
    },

    async pcRefreshConnectionStatus() {
      try {
        this.pcConnectionStatus = await GetConnectionStatus()
        if (this.pcConnectionStatus && !this.pcConnectionStatus.connected) {
          this.pcGuestError = this.pcConnectionStatus.disconnect_reason || '连接已断开'
        }
      } catch (e) {
        // Silent failure - don't interrupt polling
      }
    },

    resetPc() {
      this.stopGuestEvents()
      this.$reset()
    },
  },
})
