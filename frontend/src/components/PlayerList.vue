<script setup lang="ts">
defineProps<{
  players: Array<{ machine_id: string; name: string; kind: 'HOST' | 'GUEST' }>
}>()
</script>

<template>
  <div class="rounded-xl border border-border bg-card p-4 space-y-3">
    <div class="flex items-center justify-between">
      <p class="text-xs text-muted-foreground uppercase tracking-wider">玩家列表</p>
      <span class="text-xs text-muted-foreground">{{ players.length }} 人</span>
    </div>

    <div v-if="players.length === 0" class="py-4 text-center text-sm text-muted-foreground">
      等待玩家加入...
    </div>

    <ul v-else class="space-y-2">
      <li
        v-for="player in players"
        :key="player.machine_id"
        class="flex items-center gap-3 rounded-lg px-3 py-2 bg-muted/50"
      >
        <div class="flex size-8 items-center justify-center rounded-full bg-primary/10 text-xs font-medium text-primary">
          {{ player.name.charAt(0).toUpperCase() }}
        </div>
        <div class="flex-1 min-w-0">
          <p class="text-sm font-medium truncate">{{ player.name }}</p>
          <p class="text-xs text-muted-foreground">{{ player.kind === 'HOST' ? '房主' : '玩家' }}</p>
        </div>
      </li>
    </ul>
  </div>
</template>
