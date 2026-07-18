import { defineStore } from 'pinia'
import {
  CreateRoom, StopRoom, GetRoomStatus,
  JoinRoom, LeaveRoom, GetConnectionStatus, CancelJoin
} from '../../bindings/gravitycone/core/protocol/paperconnect/paperconnectservice.js'

export const usePaperConnectStore = defineStore('paperconnect', {
  state: () => ({
    // HOST
    pcRoomStatus: null,
    pcCreating: false,
    pcHostError: '',
    // GUEST
    pcConnectionStatus: null,
    pcJoining: false,
    pcGuestError: '',
  }),

  getters: {
    isHostingPc: (state) => state.pcRoomStatus?.running ?? false,
    isConnectedPc: (state) => state.pcConnectionStatus?.connected ?? false,
    hostRoomCodePc: (state) => state.pcRoomStatus?.code ?? '',
  },

  actions: {
    async pcCreateRoom(playerName) {
      this.pcCreating = true
      this.pcHostError = ''
      try {
        const result = await CreateRoom(playerName, '')
        this.pcRoomStatus = result
        return result
      } catch (e) {
        this.pcHostError = e?.message || String(e)
        throw e
      } finally {
        this.pcCreating = false
      }
    },

    async pcStopRoom() {
      try {
        await StopRoom()
      } catch (e) {
        this.pcHostError = e?.message || String(e)
      }
      this.pcRoomStatus = null
    },

    async pcRefreshRoomStatus() {
      try {
        this.pcRoomStatus = await GetRoomStatus()
      } catch (e) {
        this.pcHostError = e?.message || String(e)
      }
    },

    async pcJoinRoom(roomCode, playerName) {
      this.pcJoining = true
      this.pcGuestError = ''
      try {
        const result = await JoinRoom(roomCode, playerName, '')
        this.pcConnectionStatus = result
        return result
      } catch (e) {
        this.pcGuestError = e?.message || String(e)
        throw e
      } finally {
        this.pcJoining = false
      }
    },

    async pcCancelJoin() {
      try {
        await CancelJoin()
      } catch { /* ignore */ }
    },

    async pcLeaveRoom() {
      try {
        await LeaveRoom()
      } catch { /* ignore */ }
      this.pcConnectionStatus = null
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
      this.$reset()
    },
  },
})
