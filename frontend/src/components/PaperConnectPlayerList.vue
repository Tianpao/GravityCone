<script setup lang="ts">
defineProps<{
  players: Array<{ player: string; isRoomHost: boolean }>
  emptyText: string
}>()
</script>

<template>
  <div v-if="players.length === 0" class="py-2 text-center text-sm text-muted-foreground">
    {{ emptyText }}
  </div>

  <ul v-else class="space-y-2">
    <li
      v-for="player in players"
      :key="player.player"
      class="flex items-center gap-3 rounded-lg bg-muted/50 px-3 py-2"
    >
      <div class="flex size-8 items-center justify-center rounded-full bg-primary/10 text-xs font-medium text-primary">
        {{ player.player.charAt(0).toUpperCase() }}
      </div>
      <div class="min-w-0 flex-1">
        <p class="truncate text-sm font-medium">{{ player.player }}</p>
        <p class="text-xs text-muted-foreground">{{ player.isRoomHost ? '房主' : '玩家' }}</p>
      </div>
    </li>
  </ul>
</template>
