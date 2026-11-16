import { ref } from 'vue'
import { defineStore } from 'pinia'
import { getDeploymentCapabilities } from '@/api/system'
import { useAuthStore } from '@/stores/auth'
import {
  isDeploymentCapabilitySupported,
  type DeploymentCapabilityKey,
  type DeploymentCapabilityMap,
} from '@/config/deploymentCapabilities'

export const useDeploymentCapabilitiesStore = defineStore('deploymentCapabilities', () => {
  const edition = ref('')
  const capabilities = ref<DeploymentCapabilityMap>({})
  const loaded = ref(false)
  const loadError = ref('')
  let loadingPromise: Promise<void> | null = null

  const ensureLoaded = async (force = false): Promise<void> => {
    if (loaded.value && !force) return
    if (loadingPromise) return loadingPromise

    loadingPromise = (async () => {
      try {
        const response = await getDeploymentCapabilities()
        edition.value = response.data?.edition || ''
        capabilities.value = response.data?.capabilities || {}
        loadError.value = ''
        loaded.value = true
      } catch (error) {
        // 能力探测失败时保持 fail-open，并允许后续路由/组件自动重试。
        capabilities.value = {}
        loadError.value = error instanceof Error ? error.message : String(error)
      } finally {
        loadingPromise = null
      }
    })()

    return loadingPromise
  }

  const reset = () => {
    edition.value = ''
    capabilities.value = {}
    loaded.value = false
    loadError.value = ''
    loadingPromise = null
  }

  const isSupported = (key?: DeploymentCapabilityKey) => {
    const authStore = useAuthStore()
    return isDeploymentCapabilitySupported(capabilities.value, key, {
      liteMode: authStore.isLiteMode,
      edition: edition.value,
    })
  }

  return {
    edition,
    capabilities,
    loaded,
    loadError,
    ensureLoaded,
    reset,
    isSupported,
  }
})
