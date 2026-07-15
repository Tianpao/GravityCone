import { defineStore } from 'pinia'
import { StartLogin, Logout, GetCurrentUser } from '@/../bindings/gravitycone/core/app/account/natayarkservice'

export interface NatayarkUser {
  id: number
  username: string
  email: string
  realname: boolean
  status: number
  last_login: string
  regtime: string
}

export const useUserStore = defineStore('user', {
  state: () => ({
    user: null as NatayarkUser | null,
    loading: false,
    initialized: false,
    error: '',
  }),
  getters: {
    isLoggedIn: (state) => state.user !== null,
  },
  actions: {
    async login() {
      this.loading = true
      this.error = ''
      try {
        const user = await StartLogin()
        this.user = user as NatayarkUser
      } catch (e: any) {
        this.error = e?.toString() || 'Login failed'
      } finally {
        this.loading = false
      }
    },
    async logout() {
      try {
        await Logout()
      } catch {}
      this.user = null
    },
    async refreshUser() {
      try {
        const user = await GetCurrentUser()
        this.user = user as NatayarkUser | null
      } catch {
        this.user = null
      } finally {
        this.initialized = true
      }
    },
  },
})
