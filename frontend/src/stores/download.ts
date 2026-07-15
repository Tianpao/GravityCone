import { defineStore } from 'pinia'
import { Events } from '@wailsio/runtime'

export interface DownloadProgress {
  step: 'downloading' | 'extracting'
  percent: number
  totalSize: number
  speed: number
}

export const useDownloadStore = defineStore('download', {
  state: () => ({
    progress: null as DownloadProgress | null,
    downloading: false,
  }),

  actions: {
    startListening() {
      Events.On('download.progress', (event: any) => {
        const data: DownloadProgress = event.data
        this.progress = data
        this.downloading = true
        if (data.step === 'extracting' && data.percent >= 100) {
          // Small delay before clearing to show "extracting 100%"
          setTimeout(() => {
            this.downloading = false
            this.progress = null
          }, 500)
        }
      })
    },
  },
})
