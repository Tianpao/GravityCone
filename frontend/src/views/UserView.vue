<script setup lang="ts">
import { useUserStore } from '@/stores/user'
import { useMinecraftStore } from '@/stores/minecraft'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'

const user = useUserStore()
const mc = useMinecraftStore()
</script>

<template>
  <div class="flex flex-1 flex-col gap-4 px-5 py-6">
    <!-- Login required alert -->
    <div
      v-if="user.loginRequired"
      class="flex items-start gap-3 rounded-lg border border-amber-500/30 bg-amber-500/10 p-4"
    >
      <svg class="mt-0.5 size-5 shrink-0 text-amber-500" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v4m0 4h.01M12 3a9 9 0 100 18 9 9 0 000-18z" />
      </svg>
      <div class="min-w-0">
        <p class="text-sm font-medium text-amber-600 dark:text-amber-400">需要登录</p>
        <p class="text-xs text-amber-600/80 dark:text-amber-400/80">请先登录 Natayark 账号以使用 GravityCone 的全部功能</p>
      </div>
    </div>

    <!-- Natayark ID -->
    <div class="flex items-center gap-3 rounded-lg border border-border p-3">
      <div class="flex size-9 shrink-0 items-center justify-center rounded-full bg-muted text-sm font-bold text-muted-foreground">
        <template v-if="user.isLoggedIn && user.user">
          {{ user.user.username.charAt(0).toUpperCase() }}
        </template>
        <template v-else>
          <svg viewBox="0 0 24 24" class="size-4.5 text-muted-foreground">
            <circle cx="12" cy="8" r="4" fill="currentColor" opacity="0.5" />
            <circle cx="12" cy="12" r="10" fill="none" stroke="currentColor" stroke-width="1.5" />
            <path d="M4 20c0-4 3.6-7 8-7s8 3 8 7" fill="currentColor" opacity="0.3" />
          </svg>
        </template>
      </div>
      <div class="min-w-0 flex-1">
        <div class="text-sm font-medium">Natayark ID</div>
        <div v-if="user.loading" class="text-xs text-muted-foreground">正在登录...</div>
        <div v-else-if="user.isLoggedIn && user.user" class="truncate text-xs text-muted-foreground">{{ user.user.username }}</div>
        <div v-else-if="user.error" class="truncate text-xs text-red-500">{{ user.error }}</div>
        <div v-else class="text-xs text-muted-foreground">未登录</div>
      </div>
      <div class="shrink-0">
        <Button v-if="user.loading" size="sm" variant="ghost" disabled>
          <div class="size-4 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" />
        </Button>
        <Button v-else-if="user.isLoggedIn" size="sm" variant="ghost" @click="user.logout()">退出</Button>
        <Button v-else size="sm" @click="user.login()">登录</Button>
      </div>
    </div>

    <!-- Temporarily disabled: Microsoft login
    <Separator />

    <div class="flex items-center gap-3 rounded-lg border border-border p-3">
      <div class="flex shrink-0 items-center justify-center bg-muted overflow-hidden" style="width: 40px; height: 32px;">
        <img v-if="mc.isLoggedIn && mc.user?.avatar_png" :src="mc.user.avatar_png" :alt="mc.user.username" class="size-full" style="image-rendering: pixelated" />
        <svg v-else viewBox="0 0 24 24" class="size-4.5 text-muted-foreground" fill="none" stroke="currentColor" stroke-width="1.5">
          <rect x="3" y="3" width="18" height="18" rx="2" />
          <rect x="8" y="3" width="4" height="4" />
          <rect x="16" y="3" width="2" height="4" />
          <rect x="8" y="11" width="4" height="4" />
          <rect x="14" y="11" width="4" height="4" />
          <rect x="3" y="17" width="4" height="4" />
          <rect x="11" y="17" width="4" height="4" />
        </svg>
      </div>
      <div class="min-w-0 flex-1">
        <div class="text-sm font-medium">Microsoft登录（在线）</div>
        <div v-if="mc.loading" class="text-xs text-muted-foreground">正在登录...</div>
        <div v-else-if="mc.isLoggedIn && mc.user" class="truncate text-xs text-muted-foreground">{{ mc.user.username }}</div>
        <div v-else-if="mc.error" class="truncate text-xs text-red-500">{{ mc.error }}</div>
        <div v-else class="text-xs text-muted-foreground">未登录</div>
      </div>
      <div class="shrink-0">
        <Button v-if="mc.loading" size="sm" variant="ghost" disabled>
          <div class="size-4 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" />
        </Button>
        <Button v-else-if="mc.isLoggedIn" size="sm" variant="ghost" @click="mc.logout()">退出</Button>
        <Button v-else size="sm" @click="mc.login()">登录</Button>
      </div>
    </div>
    -->
  </div>
</template>
