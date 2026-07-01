<script setup lang="ts">
import { ref, computed } from 'vue'
import { useWatermarkStore } from '@/stores/watermark'
import { Button } from '@/components/ui/button'
import { CopyOutline, CheckmarkOutline, RefreshOutline, ImageOutline } from '@vicons/ionicons5'

const props = defineProps<{
  roomCode: string
}>()

const watermark = useWatermarkStore()

const selectedIndex = ref(0)
const previewBase64 = ref('')
const copied = ref(false)

const hasDemoImages = computed(() => watermark.demoImages.length > 0)
const currentImageName = computed(() => {
  if (!watermark.demoImages[selectedIndex.value]) return ''
  const parts = watermark.demoImages[selectedIndex.value].replace(/\\/g, '/').split('/')
  return parts[parts.length - 1]
})

async function generate() {
  const source = watermark.demoImages[selectedIndex.value]
  if (!source) return
  const result = await watermark.encode(source, props.roomCode)
  if (result) {
    previewBase64.value = result.base64_png
  }
}

function prevImage() {
  if (watermark.demoImages.length > 1) {
    selectedIndex.value = (selectedIndex.value - 1 + watermark.demoImages.length) % watermark.demoImages.length
  }
}

function nextImage() {
  if (watermark.demoImages.length > 1) {
    selectedIndex.value = (selectedIndex.value + 1) % watermark.demoImages.length
  }
}

async function copyToClipboard() {
  if (!previewBase64.value) return
  try {
    const byteString = atob(previewBase64.value)
    const ab = new ArrayBuffer(byteString.length)
    const ia = new Uint8Array(ab)
    for (let i = 0; i < byteString.length; i++) {
      ia[i] = byteString.charCodeAt(i)
    }
    const blob = new Blob([ab], { type: 'image/png' })
    await navigator.clipboard.write([
      new ClipboardItem({ 'image/png': blob })
    ])
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch (e) {
    console.error('Clipboard write failed:', e)
  }
}
</script>

<template>
  <div class="rounded-xl border border-border bg-card p-5 space-y-4">
    <div class="flex items-center gap-2">
      <div class="flex size-8 items-center justify-center rounded-full bg-primary/10">
        <ImageOutline class="size-4 text-primary" />
      </div>
      <div>
        <p class="text-sm font-medium">联机图片分享</p>
        <p class="text-xs text-muted-foreground">将房间代码嵌入图片后分享</p>
      </div>
    </div>

    <!-- No demo images warning -->
    <div
      v-if="!hasDemoImages && !watermark.loadingImages"
      class="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-400"
    >
      未检测到演示图片。请在项目根目录创建 <code class="rounded bg-amber-500/20 px-1">images</code> 文件夹并放入图片。
    </div>

    <!-- Image selector -->
    <div v-if="hasDemoImages" class="space-y-3">
      <div class="flex items-center gap-2">
        <Button variant="ghost" size="xs" class="h-7 w-7 p-0" @click="prevImage">&larr;</Button>
        <span class="flex-1 text-center text-xs text-muted-foreground truncate">{{ currentImageName }}</span>
        <Button variant="ghost" size="xs" class="h-7 w-7 p-0" @click="nextImage">&rarr;</Button>
        <Button variant="outline" size="xs" class="h-7 gap-1" @click="watermark.loadDemoImages()">
          <RefreshOutline class="size-3" />
        </Button>
      </div>

      <Button
        class="w-full gap-2"
        :disabled="watermark.encoding"
        @click="generate"
      >
        <div v-if="watermark.encoding" class="size-4 animate-spin rounded-full border-2 border-current border-t-transparent" />
        <span>{{ watermark.encoding ? '生成中...' : '生成联机图片' }}</span>
      </Button>
    </div>

    <div v-else-if="watermark.loadingImages" class="flex justify-center py-4">
      <div class="size-5 animate-spin rounded-full border-2 border-muted-foreground border-t-transparent" />
    </div>

    <!-- Error -->
    <div
      v-if="watermark.error"
      class="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive"
    >
      {{ watermark.error }}
    </div>

    <!-- Preview -->
    <div v-if="previewBase64" class="space-y-3">
      <div class="overflow-hidden rounded-lg border border-border">
        <img
          :src="'data:image/png;base64,' + previewBase64"
          alt="联机图片预览"
          class="w-full object-cover"
        />
      </div>
      <Button variant="outline" size="sm" class="w-full gap-2" @click="copyToClipboard">
        <component :is="copied ? CheckmarkOutline : CopyOutline" class="size-4" />
        <span>{{ copied ? '已复制到剪贴板' : '复制图片到剪贴板' }}</span>
      </Button>
    </div>
  </div>
</template>
