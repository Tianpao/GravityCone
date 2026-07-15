import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ListDemoImages, EncodeRoomCode, DecodeRoomCode } from '@/../bindings/gravitycone/core/app/watermarkservice'

// This interface matches the Go WatermarkResult struct - it will also be in auto-generated bindings
interface WatermarkResult {
  output_path: string
  base64_png: string
}

export const useWatermarkStore = defineStore('watermark', () => {
  // State
  const demoImages = ref<string[]>([])
  const loadingImages = ref(false)
  const encoding = ref(false)
  const decoding = ref(false)
  const lastResult = ref<WatermarkResult | null>(null)
  const decodedRoomCode = ref('')
  const error = ref('')

  async function loadDemoImages() {
    loadingImages.value = true
    error.value = ''
    try {
      demoImages.value = (await ListDemoImages()) ?? []
    } catch (e: any) {
      error.value = e?.message || String(e)
    } finally {
      loadingImages.value = false
    }
  }

  async function encode(sourcePath: string, roomCode: string): Promise<WatermarkResult | null> {
    encoding.value = true
    error.value = ''
    try {
      const result = await EncodeRoomCode(sourcePath, roomCode)
      lastResult.value = result
      return result
    } catch (e: any) {
      error.value = e?.message || String(e)
      return null
    } finally {
      encoding.value = false
    }
  }

  async function decode(imageBase64: string): Promise<string | null> {
    decoding.value = true
    error.value = ''
    decodedRoomCode.value = ''
    try {
      const code = await DecodeRoomCode(imageBase64)
      decodedRoomCode.value = code
      return code
    } catch (e: any) {
      error.value = e?.message || String(e)
      return null
    } finally {
      decoding.value = false
    }
  }

  function clearError() {
    error.value = ''
  }

  function reset() {
    demoImages.value = []
    lastResult.value = null
    decodedRoomCode.value = ''
    error.value = ''
  }

  return {
    demoImages,
    loadingImages,
    encoding,
    decoding,
    lastResult,
    decodedRoomCode,
    error,
    loadDemoImages,
    encode,
    decode,
    clearError,
    reset,
  }
})
