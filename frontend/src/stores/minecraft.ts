import { defineStore } from 'pinia'
import { StartLogin, Logout, GetCurrentUser } from '@/../bindings/gravitycone/core/app/account/minecraftservice'

export interface MinecraftUser {
  username: string
  uuid: string
  access_token: string
  avatar_png: string
}

export const useMinecraftStore = defineStore('minecraft', {
  state: () => ({
    user: null as MinecraftUser | null,
    loading: false,
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
        this.user = user as MinecraftUser
      } catch (e: any) {
        this.error = e?.toString() || 'Minecraft login failed'
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
        this.user = user as MinecraftUser | null
      } catch {}
    },
  },
})
