<script setup lang="ts">
import TitleBar from "@/components/TitleBar.vue"
import BottomNav from "@/components/BottomNav.vue"
import { useUserStore } from "@/stores/user"

const user = useUserStore()
user.refreshUser()
</script>

<template>
  <div class="flex h-screen flex-col overflow-hidden">
    <TitleBar />
    <main class="flex flex-1 flex-col overflow-auto">
      <RouterView />
    </main>
    <BottomNav />
  </div>
</template>
