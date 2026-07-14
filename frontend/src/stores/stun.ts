import { defineStore } from 'pinia'
import { TestStun } from '@/../bindings/gravitycone/core/stunservice'

type HoleGrade = 'excellent' | 'good' | 'poor' | 'unavailable'

export interface NatInfo {
  grade: HoleGrade
  label: string
  color: string
  tooltip: string
}

const natTypeMap: Record<number, NatInfo> = {
  1: { grade: 'excellent', label: '优', color: 'text-green-500', tooltip: 'No PAT / 开放型互联网' },
  2: { grade: 'good', label: '良', color: 'text-blue-500', tooltip: 'Symmetric Firewall / 对称型防火墙' },
  3: { grade: 'good', label: '良', color: 'text-blue-500', tooltip: 'Full Cone NAT / 完全圆锥型NAT' },
  4: { grade: 'poor', label: '差', color: 'text-yellow-500', tooltip: 'Restricted Cone NAT / 受限圆锥型NAT' },
  5: { grade: 'poor', label: '差', color: 'text-yellow-500', tooltip: 'Port Restricted Cone NAT / 端口受限圆锥型NAT' },
  6: { grade: 'unavailable', label: '不可用', color: 'text-red-500', tooltip: 'Symmetric Increment / 对称型递增NAT' },
  7: { grade: 'unavailable', label: '不可用', color: 'text-red-500', tooltip: 'Symmetric NAT / 对称型NAT' },
}

const pendingInfo: NatInfo = { grade: 'excellent', label: '检测中', color: 'text-muted-foreground', tooltip: '' }
const failInfo: NatInfo = { grade: 'unavailable', label: '不可用', color: 'text-red-500', tooltip: 'Test Failed / 测试失败' }

export const useStunStore = defineStore('stun', {
  state: () => ({
    udpNat: { ...pendingInfo } as NatInfo,
    tcpNat: { ...pendingInfo } as NatInfo,
    ipv6Enabled: false,
    tested: false,
  }),
  actions: {
    async testStun() {
      if (this.tested) return
      this.tested = true
      try {
        const result = await TestStun()
        if (result) {
          this.udpNat = natTypeMap[result.udp_nat_type] ?? pendingInfo
          this.tcpNat = natTypeMap[result.tcp_nat_type] ?? pendingInfo
          this.ipv6Enabled = result.public_ip?.some(ip => ip.includes(':')) ?? false
        }
      } catch {
        this.udpNat = { ...failInfo }
        this.tcpNat = { ...failInfo }
      }
    },
  },
})
