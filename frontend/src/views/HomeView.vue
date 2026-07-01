<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { GlobeOutline } from '@vicons/ionicons5'
import { useStunStore } from '@/stores/stun'
import CreateRoomDialog from '@/components/CreateRoomDialog.vue'
import JoinRoomDialog from '@/components/JoinRoomDialog.vue'

const stun = useStunStore()
const showCreateDialog = ref(false)
const showJoinDialog = ref(false)

onMounted(() => {
  stun.testStun()
})
</script>

<template>
  <TooltipProvider :delay-duration="500">
    <div class="flex flex-1 flex-col items-center justify-center gap-8">
      <div class="flex flex-col items-center">
        <img src="/appicon.png" alt="Logo" class="h-16 w-16" />
        <h1 class="mt-3 text-2xl font-bold">GravityCone</h1>
      </div>
      <div class="flex flex-col items-center gap-4">
        <Button size="lg" class="text-lg px-8 py-6" @click="showCreateDialog = true">创建房间</Button>
        <Button variant="outline" size="lg" class="text-lg px-8 py-6" @click="showJoinDialog = true">加入房间</Button>
      </div>
      <div class="flex flex-col items-center gap-1 text-xs text-muted-foreground">
        <div class="flex items-center gap-3">
          <GlobeOutline class="size-3.5" />
          <span>IPv4</span>
          <Tooltip>
            <TooltipTrigger as-child>
              <span>UDP <span :class="stun.udpNat.color">{{ stun.udpNat.label }}</span></span>
            </TooltipTrigger>
            <TooltipContent>{{ stun.udpNat.tooltip }}</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger as-child>
              <span>TCP <span :class="stun.tcpNat.color">{{ stun.tcpNat.label }}</span></span>
            </TooltipTrigger>
            <TooltipContent>{{ stun.tcpNat.tooltip }}</TooltipContent>
          </Tooltip>
          <span>IPv6 <span :class="stun.ipv6Enabled ? 'text-green-500' : 'text-red-500'">{{ stun.ipv6Enabled ? '已开启' : '未开启' }}</span></span>
        </div>
      </div>
      <CreateRoomDialog v-model:open="showCreateDialog" />
      <JoinRoomDialog v-model:open="showJoinDialog" />
    </div>
  </TooltipProvider>
</template>
