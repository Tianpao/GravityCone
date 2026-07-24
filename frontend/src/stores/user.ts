import { defineStore } from 'pinia'
import { StartLogin, Logout, RestoreSession, GetCurrentUser } from '@/../bindings/gravitycone/core/app/account/natayarkservice'

const COOKIE_KEY = 'gc_naids_token'
const COOKIE_MAX_AGE = 7 * 24 * 60 * 60 // 7 days

function getCookieToken(): string {
  const match = document.cookie.match(new RegExp(`(?:^|;\\s*)${COOKIE_KEY}=([^;]*)`))
  return match ? decodeURIComponent(match[1]) : ''
}

function setCookieToken(token: string) {
  document.cookie = `${COOKIE_KEY}=${encodeURIComponent(token)}; max-age=${COOKIE_MAX_AGE}; SameSite=Strict; path=/`
}

function deleteCookieToken() {
  document.cookie = `${COOKIE_KEY}=; max-age=0; path=/`
}

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
    loginRequired: false,
  }),
  getters: {
    isLoggedIn: (state) => state.user !== null,
  },
  actions: {
    async login() {
      this.loading = true
      this.error = ''
      try {
        const result = await StartLogin()
        if (result) {
          this.user = result.user as NatayarkUser
          setCookieToken(result.access_token)
          this.loginRequired = false
        }
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
      deleteCookieToken()
      this.user = null
    },
    async refreshUser() {
      try {
        const token = getCookieToken()
        if (token) {
          const user = await RestoreSession(token)
          if (user) {
            this.user = user as NatayarkUser
          } else {
            deleteCookieToken()
            this.user = null
          }
        } else {
          this.user = null
        }
      } catch {
        deleteCookieToken()
        this.user = null
      } finally {
        this.initialized = true
      }
    },
  },
})
