<template>
  <div class="login-layout">
    <div class="animated-bg">
      <div class="knowledge-node node-1">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" />
          <path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" />
        </svg>
      </div>
      <div class="knowledge-node node-2">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
        </svg>
      </div>
      <div class="knowledge-node node-3">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M12 2L2 7l10 5 10-5-10-5z" />
          <path d="M2 17l10 5 10-5" />
          <path d="M2 12l10 5 10-5" />
        </svg>
      </div>
      <div class="knowledge-node node-4">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <ellipse cx="12" cy="5" rx="9" ry="3" />
          <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3" />
          <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5" />
        </svg>
      </div>
      <div class="knowledge-node node-5">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="11" cy="11" r="8" />
          <path d="m21 21-4.35-4.35" />
        </svg>
      </div>
      <div class="knowledge-node node-6">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path
            d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z" />
          <polyline points="3.27 6.96 12 12.01 20.73 6.96" />
          <line x1="12" y1="22.08" x2="12" y2="12" />
        </svg>
      </div>
      <div class="knowledge-node node-7">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
          <polyline points="14 2 14 8 20 8" />
        </svg>
      </div>
      <div class="knowledge-node node-8">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
          <circle cx="9" cy="7" r="4" />
          <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
          <path d="M16 3.13a4 4 0 0 1 0 7.75" />
        </svg>
      </div>
      <div class="knowledge-node node-9">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
        </svg>
      </div>
      <div class="knowledge-node node-10">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="3" />
          <path
            d="M12 1v6m0 6v6M5.64 5.64l4.24 4.24m4.24 4.24l4.24 4.24M1 12h6m6 0h6M5.64 18.36l4.24-4.24m4.24-4.24l4.24-4.24" />
        </svg>
      </div>
      <div class="knowledge-node node-11">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M9 11l3 3L22 4" />
          <path d="M21 12v7a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11" />
        </svg>
      </div>
      <div class="knowledge-node node-12">
        <svg class="node-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polygon
            points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2" />
        </svg>
      </div>

      <svg class="knowledge-lines" viewBox="0 0 100 100" preserveAspectRatio="none">
        <line class="connection-line line-1" x1="20" y1="15" x2="35" y2="25" />
        <line class="connection-line line-2" x1="35" y1="25" x2="55" y2="20" />
        <line class="connection-line line-3" x1="55" y1="20" x2="85" y2="12" />
        <line class="connection-line line-4" x1="8" y1="35" x2="25" y2="45" />
        <line class="connection-line line-5" x1="25" y1="45" x2="65" y2="48" />
        <line class="connection-line line-6" x1="20" y1="60" x2="60" y2="75" />
        <line class="connection-line line-7" x1="20" y1="15" x2="20" y2="60" />
        <line class="connection-line line-8" x1="55" y1="20" x2="45" y2="50" />
        <line class="connection-line line-9" x1="65" y1="48" x2="90" y2="38" />
        <line class="connection-line line-10" x1="40" y1="70" x2="75" y2="80" />
        <line class="connection-line line-11" x1="35" y1="25" x2="25" y2="45" />
        <line class="connection-line line-12" x1="75" y1="30" x2="65" y2="48" />
      </svg>
    </div>

    <!-- Logo - Top Left -->
    <a v-if="githubUrl || websiteUrl" :href="githubUrl || websiteUrl" target="_blank" rel="noopener noreferrer" class="header-logo" :title="$t('common.website')">
      <img src="@/assets/img/genyee.png" alt="GENYEE" class="logo-image" />
    </a>
    <div v-else class="header-logo">
      <img src="@/assets/img/genyee.png" alt="GENYEE" class="logo-image" />
    </div>

    <!-- Header Links - Top Right -->
    <div class="header-links">
      <a v-if="websiteUrl" :href="websiteUrl" target="_blank" rel="noopener noreferrer" class="header-link" :title="$t('common.website')" :aria-label="$t('common.website')">
        <svg aria-hidden="true" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"
          stroke-linecap="round">
          <circle cx="12" cy="12" r="10" />
          <line x1="2" y1="12" x2="22" y2="12" />
          <path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z" />
        </svg>
        <span class="link-text">{{ $t('common.website') }}</span>
      </a>

      <a v-if="githubUrl" :href="githubUrl" target="_blank" rel="noopener noreferrer" class="header-link" :title="$t('common.info')" aria-label="GitHub">
        <svg aria-hidden="true" width="17" height="17" viewBox="0 0 24 24" fill="currentColor">
          <path
            d="M12 0C5.37 0 0 5.37 0 12c0 5.31 3.435 9.795 8.205 11.385.6.105.825-.255.825-.57 0-.285-.015-1.23-.015-2.235-3.015.555-3.795-.735-4.035-1.41-.135-.345-.72-1.41-1.23-1.695-.42-.225-1.02-.78-.015-.795.945-.015 1.62.87 1.845 1.23 1.08 1.815 2.805 1.305 3.495.99.105-.78.42-1.305.765-1.605-2.67-.3-5.46-1.335-5.46-5.925 0-1.305.465-2.385 1.23-3.225-.12-.3-.54-1.53.12-3.18 0 0 1.005-.315 3.3 1.23.96-.27 1.98-.405 3-.405s2.04.135 3 .405c2.295-1.56 3.3-1.23 3.3-1.23.66 1.65.24 2.88.12 3.18.765.84 1.23 1.905 1.23 3.225 0 4.605-2.805 5.625-5.475 5.925.435.375.81 1.095.81 2.22 0 1.605-.015 2.895-.015 3.3 0 .315.225.69.825.57A12.02 12.02 0 0 0 24 12c0-6.63-5.37-12-12-12z" />
        </svg>
        <span class="link-text">GitHub</span>
      </a>

      <div class="language-switch">
        <button
          type="button"
          class="header-link"
          :title="currentLangOption?.label"
          :aria-label="currentLangOption?.label || $t('language.title')"
          :aria-expanded="showLanguageMenu"
          @click="toggleLanguageMenu"
        >
          <span class="lang-flag-icon" aria-hidden="true">{{ currentLangOption?.flag }}</span>
          <span class="link-text">{{ currentLangOption?.shortLabel }}</span>
          <svg aria-hidden="true" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"
            stroke-linecap="round">
            <polyline points="6 9 12 15 18 9" />
          </svg>
        </button>

        <!-- Language Dropdown -->
        <div v-if="showLanguageMenu" class="language-dropdown">
          <button
            v-for="lang in languageOptions"
            :key="lang.value"
            type="button"
            class="language-option"
            :class="{ active: currentLanguage === lang.value }"
            @click="selectLanguage(lang.value)"
          >
            <span class="lang-flag" aria-hidden="true">{{ lang.flag }}</span>
            <span class="lang-label">{{ lang.label }}</span>
            <span v-if="currentLanguage === lang.value" class="check-icon" aria-hidden="true">✓</span>
          </button>
        </div>
      </div>
    </div>

    <!-- Left Showcase Section -->
    <div class="showcase-section">
      <div class="showcase-content">
        <p class="showcase-subtitle">{{ $t('platform.subtitle') }}</p>
        <p class="showcase-description">{{ $t('platform.description') }}</p>

        <div class="feature-tags">
          <span class="tag">{{ $t('platform.rag') }}</span>
          <span class="tag">{{ $t('platform.agent') }}</span>
          <span class="tag">{{ $t('platform.wiki') }}</span>
          <span class="tag">{{ $t('platform.hybridSearch') }}</span>
        </div>

        <!-- Swiper Carousel -->
        <div class="carousel-container">
          <swiper :modules="modules" :slides-per-view="1" :loop="true" :autoplay="{
            delay: 4000,
            disableOnInteraction: false,
          }" :effect="'fade'" :fade-effect="{ crossFade: true }"
            :pagination="{ clickable: true, dynamicBullets: false }" :speed="800" class="screenshot-swiper">
            <swiper-slide v-for="(slide, index) in slides" :key="index">
              <div class="slide-content">
                <img :src="slide.image" :alt="slide.title" class="slide-image" />
              </div>
            </swiper-slide>
          </swiper>
        </div>
      </div>
    </div>

    <!-- Right Form Section -->
    <div class="form-section">
      <div class="form-panel">
        <!-- Login Card -->
        <div class="form-card" v-if="!isRegisterMode">
          <!-- 登录已有账号时保留邀请上下文。 -->
          <div v-if="inviteLookup" class="invite-banner">
            <t-icon name="link" class="invite-banner__icon" />
            <div class="invite-banner__text">
              <div class="invite-banner__title">
                {{ $t('inviteRegister.bannerTitle', { tenant: inviteLookup.tenant_name || '' }) }}
              </div>
              <div class="invite-banner__hint">
                {{ $t('inviteRegister.bannerHintLogin') }}
              </div>
            </div>
          </div>
          <div v-else-if="inviteLookupError" class="invite-banner invite-banner--error">
            {{ inviteLookupError }}
          </div>
          <div class="form-header">
            <h2 class="form-title">{{ $t('auth.login') }}</h2>
            <p class="form-welcome">{{ $t('auth.subtitle') }}</p>
            <p v-if="registrationEnabled" class="form-hint">{{ $t('auth.loginHint') }}</p>
          </div>

          <div class="form-content">
            <t-form ref="formRef" :data="formData" :rules="formRules" @submit="handleLogin" layout="vertical"
              label-align="top">
              <t-form-item :label="$t('auth.email')" name="email">
                <t-input v-model="formData.email" :placeholder="$t('auth.emailPlaceholder')" type="text"
                  autocomplete="email" size="large" :disabled="loading" />
              </t-form-item>

              <t-form-item :label="$t('auth.password')" name="password">
                <t-input v-model="formData.password" :placeholder="$t('auth.passwordPlaceholder')" type="password"
                  autocomplete="current-password" size="large" :disabled="loading" @enter="handleLogin" />
              </t-form-item>

              <t-button type="submit" theme="primary" size="large" block :loading="loading" class="submit-button">
                {{ loading ? $t('auth.loggingIn') : $t('auth.login') }}
              </t-button>

              <div class="register-cta" v-if="registrationEnabled">
                <div class="register-cta__divider">
                  <span>{{ $t('auth.firstTime') }}</span>
                </div>
                <t-button theme="default" variant="outline" size="large" block class="register-cta__button"
                  :disabled="loading" @click="toggleMode">
                  {{ $t('auth.createAccount') }}
                </t-button>
              </div>

              <div v-if="oidcEnabled" class="oidc-divider">
                <span>{{ $t('auth.orContinueWith') }}</span>
              </div>

              <t-button v-if="oidcEnabled" theme="default" size="large" block :loading="oidcLoading" :disabled="loading"
                class="oidc-button" @click="handleOIDCLogin">
                {{ oidcLoading ? $t('auth.redirectingToOIDC') : oidcLoginText }}
              </t-button>
            </t-form>

            <!-- Features list -->
            <div class="login-features">
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.multimodalParsing') }}</span>
              </div>
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.hybridSearchEngine') }}</span>
              </div>
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.ragQandA') }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Registration must be allowed for this mode and invitation. -->
        <div class="form-card" v-if="isRegisterMode && registrationEnabled">
          <!-- Share-link banner: shown only when ?token= resolved to a
               real invitation row. Sits above the form header so the
               invitee instantly sees who invited them and into which
               workspace, without bumping the existing register UX. -->
          <div v-if="inviteLookup" class="invite-banner">
            <t-icon name="link" class="invite-banner__icon" />
            <div class="invite-banner__text">
              <div class="invite-banner__title">
                {{ $t('inviteRegister.bannerTitle', { tenant: inviteLookup.tenant_name || '' }) }}
              </div>
              <div class="invite-banner__hint">
                {{ $t('inviteRegister.bannerHint') }}
              </div>
            </div>
          </div>
          <div v-else-if="inviteLookupError" class="invite-banner invite-banner--error">
            {{ inviteLookupError }}
          </div>
          <div class="form-header">
            <h2 class="form-title">{{ $t('auth.createAccount') }}</h2>
            <p class="form-subtitle">{{ $t('auth.registerSubtitle') }}</p>
          </div>

          <div class="form-content">
            <t-form ref="registerFormRef" :data="registerData" :rules="registerRules" @submit="handleRegister"
              layout="vertical" label-align="top">
              <t-form-item :label="$t('auth.username')" name="username">
                <t-input v-model="registerData.username" :placeholder="$t('auth.usernamePlaceholder')" size="large"
                  :disabled="loading" />
              </t-form-item>

              <t-form-item :label="$t('auth.email')" name="email">
                <t-input v-model="registerData.email" :placeholder="$t('auth.emailPlaceholder')" type="text"
                  autocomplete="email" size="large" :disabled="loading" />
              </t-form-item>

              <t-form-item :label="$t('auth.password')" name="password">
                <t-input v-model="registerData.password" :placeholder="$t('auth.passwordPlaceholder')" type="password"
                  autocomplete="new-password" size="large" :disabled="loading" />
              </t-form-item>

              <t-form-item :label="$t('auth.confirmPassword')" name="confirmPassword">
                <t-input v-model="registerData.confirmPassword" :placeholder="$t('auth.confirmPasswordPlaceholder')"
                  type="password" autocomplete="new-password" size="large" :disabled="loading"
                  @enter="handleRegister" />
              </t-form-item>

              <t-button type="submit" theme="primary" size="large" block :loading="loading" class="submit-button">
                {{ loading ? $t('auth.registering') : $t('auth.register') }}
              </t-button>
            </t-form>

            <div class="form-footer">
              <span>{{ $t('auth.haveAccount') }}</span>
              <a href="#" @click.prevent="toggleMode" class="link-button">
                {{ $t('auth.backToLogin') }}
              </a>
            </div>

            <!-- Features list for register -->
            <div class="login-features">
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.independentTenant') }}</span>
              </div>
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.fullApiAccess') }}</span>
              </div>
              <div class="feature-item">
                <span class="feature-icon">✓</span>
                <span class="feature-text">{{ $t('platform.knowledgeBaseManagement') }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, nextTick, onMounted, onBeforeUnmount, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin, type FormInstanceFunctions } from 'tdesign-vue-next'
import { useRoleLabel } from '@/composables/useRoleLabel'
import { docsUrl } from '@/utils/docsUrl'
import { notifyLoginSuccess } from '@/utils/loginNotify'
import { canRegister } from './registrationPolicy'
import { newPasswordRules } from '@/utils/passwordPolicy'
import { Swiper, SwiperSlide } from 'swiper/vue'
import { Autoplay, EffectFade, Pagination } from 'swiper/modules'
import 'swiper/css'
import 'swiper/css/effect-fade'
import 'swiper/css/pagination'
import {
  login,
  register,
  getOIDCAuthorizationURL,
  getOIDCConfig,
  autoSetup,
  getAuthConfig,
  userInfoFromApi,
  getInvitationByToken,
  registerByInvite,
  type InviteLookup,
} from '@/api/auth'
import { useAuthStore } from '@/stores/auth'
import { useI18n } from 'vue-i18n'
import { getApiBaseUrl } from '@/utils/api-base'

// Import screenshot images
import screenshot1 from '@/assets/img/screenshot-1.svg'
import screenshot2 from '@/assets/img/screenshot-2.svg'
import screenshot3 from '@/assets/img/screenshot-3.svg'
import screenshot4 from '@/assets/img/screenshot-4.svg'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const { t, tm, locale } = useI18n()
const { formatRole, roleIcon } = useRoleLabel()

const safeHttpUrl = (raw: unknown): string => {
  if (typeof raw !== 'string') return ''
  const trimmed = raw.trim()
  if (!trimmed) return ''
  try {
    const base = typeof window !== 'undefined' ? window.location?.origin : undefined
    const parsed = new URL(trimmed, base && base !== 'null' ? base : 'https://localhost')
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? trimmed : ''
  } catch {
    return ''
  }
}

type RuntimeConfigWindow = Omit<Window, '__RUNTIME_CONFIG__'> & {
  __RUNTIME_CONFIG__?: {
    WEBSITE_URL?: unknown
    GITHUB_URL?: unknown
  }
}

const runtimeConfig = typeof window !== 'undefined' ? (window as RuntimeConfigWindow).__RUNTIME_CONFIG__ : undefined
const websiteUrl = computed(() => {
  const configured = runtimeConfig?.WEBSITE_URL
  if (typeof configured === 'string') {
    const trimmed = configured.trim()
    if (!trimmed) return ''
    return safeHttpUrl(trimmed) || docsUrl('home')
  }
  return docsUrl('home')
})
const githubUrl = computed(() => safeHttpUrl(runtimeConfig?.GITHUB_URL))

// Swiper modules
const modules = [Autoplay, EffectFade, Pagination]

// Carousel slides data
const slides = [
  {
    image: screenshot4,
    title: t('platform.carousel.agenticRagTitle'),
    description: t('platform.carousel.agenticRagDesc')
  },
  {
    image: screenshot2,
    title: t('platform.carousel.hybridSearchTitle'),
    description: t('platform.carousel.hybridSearchDesc')
  },
  {
    image: screenshot3,
    title: t('platform.carousel.wikiTitle'),
    description: t('platform.carousel.wikiDesc')
  },
  {
    image: screenshot1,
    title: t('platform.carousel.smartDocRetrievalTitle'),
    description: t('platform.carousel.smartDocRetrievalDesc')
  }
]

// Form references
const formRef = ref<FormInstanceFunctions | null>(null)
const registerFormRef = ref<FormInstanceFunctions | null>(null)

// State management
const loading = ref(false)
const oidcLoading = ref(false)
const isRegisterMode = ref(false)
const showLanguageMenu = ref(false)
const oidcEnabled = ref(false)
const oidcProviderName = ref('')
// Wait for configuration before showing a registration entry.
const registrationMode = ref('')
const registrationEnabled = computed(() => canRegister(registrationMode.value, !!inviteLookup.value))
const complexPasswordEnabled = ref(false)

// invite-link state. When the URL carries ?token=xxx we resolve it to
// the originating tenant + role and switch the form into a "register
// via invitation" mode when invitation registration is enabled. Submitting the register
// form with this set hits /auth/register-by-invite (auto-login on
// success) instead of /auth/register.
const inviteToken = ref('')
const inviteLookup = ref<InviteLookup | null>(null)
const inviteLookupError = ref('')
const inviteLookupLoading = ref(false)

// Language options
const languageOptions = computed(() => [
  { value: 'zh-CN', label: t('language.autonyms.zhCN'), shortLabel: t('language.shortAutonyms.zhCN'), flag: t('language.flags.zhCN') },
  { value: 'zh-TW', label: t('language.autonyms.zhTW'), shortLabel: t('language.shortAutonyms.zhTW'), flag: t('language.flags.zhTW') },
  { value: 'en-US', label: t('language.autonyms.enUS'), shortLabel: t('language.shortAutonyms.enUS'), flag: t('language.flags.enUS') },
  { value: 'ru-RU', label: t('language.autonyms.ruRU'), shortLabel: t('language.shortAutonyms.ruRU'), flag: t('language.flags.ruRU') },
  { value: 'ko-KR', label: t('language.autonyms.koKR'), shortLabel: t('language.shortAutonyms.koKR'), flag: t('language.flags.koKR') },
  { value: 'ja-JP', label: t('language.autonyms.jaJP'), shortLabel: t('language.shortAutonyms.jaJP'), flag: t('language.flags.jaJP') },
])

const currentLanguage = computed(() => locale.value)
const oidcLoginText = computed(() => {
  if (oidcProviderName.value) {
    return t('auth.oidcLoginWithProvider', { provider: oidcProviderName.value })
  }
  return t('auth.oidcLogin')
})
const currentLangOption = computed(() => languageOptions.value.find(l => l.value === currentLanguage.value))

// Login form data
const formData = reactive<{ email: string; password: string }>({
  email: '',
  password: '',
})

// Register form data
const registerData = reactive<{
  username: string
  email: string
  password: string
  confirmPassword: string
}>({
  username: '',
  email: '',
  password: '',
  confirmPassword: '',
})

const resetRegisterData = () => {
  registerData.username = ''
  registerData.email = ''
  registerData.password = ''
  registerData.confirmPassword = ''
}

// Login form validation rules
const formRules = computed(() => ({
  email: [
    { required: true, message: t('auth.emailRequired'), type: 'error' },
    { email: true, message: t('auth.emailInvalid'), type: 'error' }
  ],
  password: [
    { required: true, message: t('auth.passwordRequired'), type: 'error' },
    { min: 8, message: t('auth.passwordMinLength'), type: 'error' },
    { max: 32, message: t('auth.passwordMaxLength'), type: 'error' }
  ],
}))

// Register form validation rules
const registerRules = computed(() => ({
  username: [
    { required: true, message: t('auth.usernameRequired'), type: 'error' },
    { min: 2, message: t('auth.usernameMinLength'), type: 'error' },
    { max: 20, message: t('auth.usernameMaxLength'), type: 'error' },
    {
      pattern: /^[a-zA-Z0-9_\u4e00-\u9fa5]+$/,
      message: t('auth.usernameInvalid'),
      type: 'error'
    }
  ],
  email: [
    { required: true, message: t('auth.emailRequired'), type: 'error' },
    { email: true, message: t('auth.emailInvalid'), type: 'error' }
  ],
  password: newPasswordRules(t, complexPasswordEnabled.value),
  confirmPassword: [
    { required: true, message: t('auth.confirmPasswordRequired'), type: 'error' },
    {
      validator: (val: string) => val === registerData.password,
      message: t('auth.passwordMismatch'),
      type: 'error'
    }
  ]
}))

// Toggle login/register mode
const toggleMode = () => {
  isRegisterMode.value = !isRegisterMode.value && registrationEnabled.value
  resetRegisterData()
}

// Toggle language menu
const toggleLanguageMenu = () => {
  showLanguageMenu.value = !showLanguageMenu.value
}

// Select language
const selectLanguage = (lang: string) => {
  locale.value = lang
  localStorage.setItem('locale', lang)
  showLanguageMenu.value = false
  MessagePlugin.success(t('language.languageSaved'))
}

// Close language menu when clicking outside
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (!target.closest('.language-switch')) {
    showLanguageMenu.value = false
  }
}

// Add click outside listener
onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleClickOutside)
})

type AuthLoginPayload = Parameters<typeof notifyLoginSuccess>[0] & {
  user?: Parameters<typeof userInfoFromApi>[0] & {
    id?: string
    tenant_id?: number | string
  }
  token?: string
  refresh_token?: string
  tenant?: {
    id?: number | string
    name?: string
    created_at?: string
    updated_at?: string
  } | null
  active_tenant?: {
    id?: number | string
    name?: string
    created_at?: string
    updated_at?: string
  } | null
  memberships?: Parameters<typeof authStore.setMemberships>[0]
}

const getErrorMessage = (err: unknown): string => {
  if (err instanceof Error) return err.message
  if (typeof err === 'object' && err !== null && 'message' in err) {
    const m = (err as { message?: unknown }).message
    if (typeof m === 'string') return m
  }
  return ''
}

const persistLoginResponse = async (response: AuthLoginPayload, skipRedirect = false) => {
  // Backend renamed `tenant` to `active_tenant` and added `memberships`
  // when tenant-level RBAC landed (issue #1303). The two are otherwise
  // identical — `active_tenant` is the tenant whose ID is encoded in the
  // JWT, defaulting to the user's home tenant on a fresh login.
  const activeTenant = response.active_tenant || response.tenant
  if (response.user && response.token) {
    // Purge any stale previous account session or cached settings before persisting new credentials.
    authStore.logout()
    try {
      localStorage.removeItem('GENYEE_settings')
    } catch {
      // Ignore storage access errors in restricted environments.
    }
    // user.tenant_id must be the user's HOME tenant (the immutable row
    // on the users table); useHomeTenant() and the home-badge logic both
    // assume so. The ACTIVE tenant (which can differ from home when the
    // server honoured a remembered last-active-tenant preference) is
    // expressed separately via setSelectedTenant below.
    const homeTenantIdRaw = response.user.tenant_id ?? activeTenant?.id ?? ''
    authStore.setUser(userInfoFromApi(response.user, homeTenantIdRaw))
    authStore.setToken(response.token)
    if (response.refresh_token) {
      authStore.setRefreshToken(response.refresh_token)
    }
    if (activeTenant) {
      authStore.setTenant({
        id: String(activeTenant.id) || '',
        name: activeTenant.name || '',
        owner_id: response.user.id || '',
        created_at: activeTenant.created_at || new Date().toISOString(),
        updated_at: activeTenant.updated_at || new Date().toISOString()
      })
    } else {
      authStore.setTenant(null)
    }
    if (Array.isArray(response.memberships)) {
      authStore.setMemberships(response.memberships)
    }
    // If the backend dropped us into a non-home tenant (honoured a
    // remembered "last active tenant" preference), set the override so
    // subsequent requests carry X-Tenant-ID and the UI stays consistent.
    // Otherwise clear any stale override left in localStorage by a
    // previous session for a different account.
    const activeIdNum = Number(activeTenant?.id)
    const homeIdNum = Number(homeTenantIdRaw)
    if (Number.isFinite(activeIdNum) && Number.isFinite(homeIdNum) && activeIdNum !== homeIdNum) {
      authStore.setSelectedTenant(activeIdNum, activeTenant?.name || null)
    } else {
      authStore.setSelectedTenant(null, null)
    }
  }

  // Pull runtime capabilities (including whether ordinary users may create
  // workspaces) before entering the main UI so create actions never flash
  // briefly when the deployment is invitation-only.
  await authStore.refreshFromAuthMe()
  await nextTick()
  if (skipRedirect) return
  router.replace(authStore.hasValidTenant ? '/platform/knowledge-bases' : '/onboarding/workspace')
}

const getBackendOIDCRedirectURI = () => `${window.location.origin}${getApiBaseUrl()}/api/v1/auth/oidc/callback`

const loadOIDCConfig = async () => {
  try {
    const response = await getOIDCConfig()
    oidcEnabled.value = !!response.success && !!response.enabled
    oidcProviderName.value = response.provider_display_name || ''
  } catch {
    oidcEnabled.value = false
    oidcProviderName.value = ''
  }
}

// Registration stays hidden if configuration cannot be loaded.
const loadAuthConfig = async () => {
  try {
    const response = await getAuthConfig()
    registrationMode.value = response.success ? response.registration_mode : ''
    complexPasswordEnabled.value = response.complex_password_enabled
  } catch {
    registrationMode.value = ''
    complexPasswordEnabled.value = false
  }
}

const handleOIDCLogin = async () => {
  try {
    oidcLoading.value = true
    const response = await getOIDCAuthorizationURL(getBackendOIDCRedirectURI())
    const authorizationURL = response.authorization_url

    const safeAuthURL = safeHttpUrl(authorizationURL)
    if (!response.success || !safeAuthURL) {
      MessagePlugin.error(response.message || t('auth.oidcLoginFailed'))
      return
    }

    // 跳转 IdP 会丢失 URL 中的 token，暂存到 sessionStorage，回调后由 App.vue 兑换。
    if (inviteToken.value) {
      sessionStorage.setItem('genyee_pending_invite_token', inviteToken.value)
    }
    window.location.href = safeAuthURL
  } catch (error: unknown) {
    console.error('OIDC login redirect failed:', error)
    MessagePlugin.error(getErrorMessage(error) || t('auth.oidcLoginFailed'))
  } finally {
    oidcLoading.value = false
  }
}

// 用 token 加入空间并进入应用。会话此时已有效，故即便 token 失效也照常进入（避免困在登录页）。
const acceptAndEnter = async (token: string) => {
  loading.value = true
  try {
    const result = await authStore.acceptInvitationByTokenAndRefresh(token)
    if (result.ok) {
      MessagePlugin.success(t('inviteRegister.joined'))
    } else {
      MessagePlugin.warning(t('inviteRegister.invalidBody'))
    }
  } catch {
    MessagePlugin.warning(t('inviteRegister.invalidBody'))
  } finally {
    loading.value = false
    await nextTick()
    router.replace('/platform/knowledge-bases')
  }
}

// Handle login
const handleLogin = async () => {
  try {
    const valid = await formRef.value?.validate()
    if (valid !== true) return

    loading.value = true

    const response = await login({
      email: formData.email,
      password: formData.password,
    })

    if (response.success) {
      if (inviteToken.value) {
        // 从邀请链接登录：持久化会话后兑换 token 并进入对应空间。
        await persistLoginResponse(response, true)
        await acceptAndEnter(inviteToken.value)
        return
      }
      await persistLoginResponse(response)
      notifyLoginSuccess(response, t, tm, formatRole, roleIcon)
    } else {
      MessagePlugin.error(response.message || t('auth.loginError'))
    }
  } catch (error: unknown) {
    console.error('Login failed:', error)
    MessagePlugin.error(getErrorMessage(error) || t('auth.loginErrorRetry'))
  } finally {
    loading.value = false
  }
}

// Handle registration. Dispatches based on whether the user arrived
// with a share-link token: with token -> register-by-invite (auto-
// login on success); without -> the normal self-service register
// (drops back to the login form for the user to sign in).
const handleRegister = async () => {
  if (!registrationEnabled.value) return
  try {
    const valid = await registerFormRef.value?.validate()
    if (valid !== true) return

    loading.value = true

    if (inviteToken.value) {
      const response = await registerByInvite({
        token: inviteToken.value,
        username: registerData.username,
        email: registerData.email,
        password: registerData.password,
      })
      if (!response.success) {
        MessagePlugin.error(response.message || t('auth.registerFailed'))
        return
      }
      MessagePlugin.success(t('auth.registerSuccess'))
      // register-by-invite returns the same shape as login (token +
      // active_tenant + memberships), so reuse the login persistence
      // path — same store writes, same redirect target.
      await persistLoginResponse(response)
      return
    }

    const response = await register({
      username: registerData.username,
      email: registerData.email,
      password: registerData.password
    })

    if (response.success) {
      MessagePlugin.success(t('auth.registerSuccess'))

      // Switch to login mode and fill in email
      isRegisterMode.value = false
      formData.email = registerData.email

      // Clear register form
      resetRegisterData()
    } else {
      MessagePlugin.error(response.message || t('auth.registerFailed'))
    }
  } catch (error: unknown) {
    console.error('Registration failed:', error)
    MessagePlugin.error(getErrorMessage(error) || t('auth.registerError'))
  } finally {
    loading.value = false
  }
}

// Check if already logged in; for lite edition, attempt transparent auto-setup
onMounted(async () => {
  // Share-link landing: ?token=xxx switches the form into invite-
  // register mode before any other auto-flow (logged-in redirect /
  // auto-setup / OIDC) gets a chance to redirect. Resolution failure
  // surfaces inline; the user can still log in normally if they
  // already have an account. We check this BEFORE the isLoggedIn
  // redirect so an existing session doesn't bounce the user to
  // /platform (and possibly back to /login if the session is stale),
  // dropping the invite token along the way.
  const tokenFromQuery = String(route.query.token || '').trim()
  if (tokenFromQuery) {
    inviteToken.value = tokenFromQuery
    inviteLookupLoading.value = true
    // 1. 先校验 token：无效/过期则停在登录页报错，不进注册模式。
    try {
      const resp = await getInvitationByToken(tokenFromQuery)
      if (resp.success && resp.data) {
        inviteLookup.value = resp.data
      } else {
        inviteLookupError.value = resp.message || t('inviteRegister.invalidBody')
        loadOIDCConfig()
        loadAuthConfig()
        return
      }
    } catch {
      inviteLookupError.value = t('inviteRegister.invalidBody')
      loadOIDCConfig()
      loadAuthConfig()
      return
    } finally {
      inviteLookupLoading.value = false
    }

    // 2. 已登录则直接兑换 token 进入空间（三种模式通用）；若会话已失效则清理残留态。
    if (authStore.isLoggedIn) {
      if (await authStore.refreshFromAuthMe()) {
        await acceptAndEnter(tokenFromQuery)
        return
      }
      authStore.logout()
    }

    // 3. Only enabled registration modes may create an invited account.
    await loadAuthConfig()
    isRegisterMode.value = registrationEnabled.value
    loadOIDCConfig()
    return
  }

  if (authStore.isLoggedIn) {
    router.replace('/platform/knowledge-bases')
    return
  }

  localStorage.removeItem('genyee_auto_setup_failed')
  try {
    const response = await autoSetup()
    if (response.success) {
      await persistLoginResponse(response, true)
      authStore.setLiteMode(true)
      router.replace(authStore.hasValidTenant ? '/platform/knowledge-bases' : '/onboarding/workspace')
      return
    }
  } catch {
    // Auto-setup may be unavailable outside the native Lite shell.
  }

  loadOIDCConfig()
  loadAuthConfig()
})
</script>

<style lang="less" scoped>
.login-layout {
  display: flex;
  width: 100%;
  min-height: 100%;
  overflow: hidden;
  position: relative;
  background: linear-gradient(
    225deg,
    var(--td-brand-color-10) 0%,
    var(--td-brand-color-9) 18%,
    var(--td-brand-color-7) 38%,
    var(--td-brand-color-5) 58%,
    var(--gy-brand) 78%,
    var(--td-brand-color-2) 100%
  );

  &::before {
    content: '';
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background:
      radial-gradient(circle at 20% 50%, color-mix(in oklab, var(--gy-text-inverse) 6%, transparent) 0%, transparent 50%),
      radial-gradient(circle at 80% 50%, color-mix(in oklab, var(--gy-text-inverse) 4%, transparent) 0%, transparent 50%);
    pointer-events: none;
  }
}

.animated-bg {
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  pointer-events: none;
  z-index: 1;
  overflow: hidden;
  contain: strict;
}

.knowledge-node {
  position: absolute;
  width: 40px;
  height: 40px;
  border-radius: var(--gy-radius-circle);
  background: color-mix(in oklab, var(--gy-text-inverse) 15%, transparent);
  border: 2px solid color-mix(in oklab, var(--gy-text-inverse) 30%, transparent);
  box-shadow: var(--gy-shadow-sm);
  display: flex;
  align-items: center;
  justify-content: center;
  animation: nodePulse 5s infinite ease-in-out;
  will-change: transform, opacity;
}

.node-icon {
  width: 20px;
  height: 20px;
  color: color-mix(in oklab, var(--gy-text-inverse) 90%, transparent);
}

.node-1 {
  top: 15%;
  left: 20%;
  animation-delay: 0s;
}

.node-2 {
  top: 25%;
  left: 35%;
  animation-delay: 0.5s;
}

.node-3 {
  top: 20%;
  left: 55%;
  animation-delay: 1s;
}

.node-4 {
  top: 30%;
  left: 75%;
  animation-delay: 1.5s;
}

.node-5 {
  top: 45%;
  left: 25%;
  animation-delay: 2s;
}

.node-6 {
  top: 50%;
  left: 45%;
  animation-delay: 2.5s;
}

.node-7 {
  top: 48%;
  left: 65%;
  animation-delay: 3s;
}

.node-8 {
  top: 60%;
  left: 20%;
  animation-delay: 0.3s;
}

.node-9 {
  top: 12%;
  right: 15%;
  animation-delay: 1.8s;
}

.node-10 {
  top: 38%;
  right: 10%;
  animation-delay: 2.3s;
}

.node-11 {
  top: 70%;
  left: 40%;
  animation-delay: 0.8s;
}

.node-12 {
  top: 65%;
  left: 80%;
  animation-delay: 1.3s;
}

@keyframes nodePulse {

  0%,
  100% {
    transform: scale(1);
    opacity: 0.65;
  }

  50% {
    transform: scale(1.08);
    opacity: 0.9;
  }
}

.knowledge-lines {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  opacity: 0.35;
}

.connection-line {
  stroke: color-mix(in oklab, var(--gy-text-inverse) 50%, transparent);
  stroke-width: 1.5;
  stroke-dasharray: 6, 3;
  stroke-linecap: round;
  animation: lineFlow 10s infinite linear;
  will-change: stroke-dashoffset;
}

.line-1 {
  animation-delay: 0s;
}

.line-2 {
  animation-delay: 0.5s;
}

.line-3 {
  animation-delay: 1s;
}

.line-4 {
  animation-delay: 0.3s;
}

.line-5 {
  animation-delay: 0.8s;
}

.line-6 {
  animation-delay: 1.3s;
}

.line-7 {
  animation-delay: 1.8s;
}

.line-8 {
  animation-delay: 2.3s;
}

.line-9 {
  animation-delay: 0.2s;
}

.line-10 {
  animation-delay: 0.7s;
}

.line-11 {
  animation-delay: 0.9s;
}

.line-12 {
  animation-delay: 1.5s;
}

@keyframes lineFlow {
  0% {
    stroke-dashoffset: 0;
  }

  100% {
    stroke-dashoffset: 18;
  }
}

@media (prefers-reduced-motion: reduce) {
  .knowledge-node {
    animation: none;
    opacity: 0.65;
  }

  .connection-line {
    animation: none;
  }
}

/* Left Showcase Section */
.showcase-section {
  flex: 0 0 52%;
  display: flex;
  align-items: flex-end;
  padding: 100px 30px 100px 50px;
  box-sizing: border-box;
  position: relative;
}

.showcase-content {
  width: 100%;
  max-width: 600px;
  position: relative;
  z-index: 2;
  display: flex;
  flex-direction: column;
  margin-bottom: 60px;
}

.showcase-subtitle {
  margin-top: 0;
  font-size: var(--gy-text-3xl);
  color: color-mix(in oklab, var(--gy-text-inverse) 95%, transparent);
  margin: 0 0 8px 0;
  font-family: var(--gy-font-sans);
  line-height: 1.4;
  font-weight: 500;
}

.showcase-description {
  font-size: var(--gy-text-lg);
  color: color-mix(in oklab, var(--gy-text-inverse) 80%, transparent);
  margin: 0 0 28px 0;
  font-family: var(--gy-font-sans);
  line-height: 1.5;
}

.feature-tags {
  display: flex;
  gap: 12px;
  margin-bottom: 40px;
  flex-wrap: wrap;
}

.tag {
  display: inline-block;
  padding: 8px 20px;
  background: color-mix(in oklab, var(--gy-text-inverse) 20%, transparent);
  border-radius: var(--gy-radius-pill);
  color: var(--gy-text-inverse);
  font-size: var(--gy-text-base);
  font-weight: 500;
  font-family: var(--gy-font-sans);
}

/* Carousel */
.carousel-container {
  width: 100%;
  margin-top: 48px;
}

.screenshot-swiper {
  width: 100%;
  border-radius: var(--gy-radius-xl);
  overflow: hidden;
  box-shadow: var(--gy-shadow-lg);
  padding-bottom: 40px;

  :deep(.swiper-wrapper) {
    transition-timing-function: cubic-bezier(0.4, 0, 0.2, 1);
  }

  :deep(.swiper-pagination) {
    bottom: 15px !important;
    z-index: 10;
  }

  :deep(.swiper-pagination-bullet) {
    width: 10px;
    height: 10px;
    background: color-mix(in oklab, var(--gy-text-inverse) 50%, transparent);
    opacity: 1;
    transition: width var(--gy-motion-slow) ease, background-color var(--gy-motion-slow) ease;
    margin: 0 6px !important;
  }

  :deep(.swiper-pagination-bullet-active) {
    background: var(--gy-surface);
    width: 28px;
    border-radius: var(--gy-radius-xs);
  }
}

.slide-content {
  width: 100%;
  height: 100%;
  background: var(--gy-surface);
  border-radius: var(--gy-radius-xl);
  overflow: hidden;
  display: flex;
  align-items: center;
  justify-content: center;
}

.slide-image {
  width: 100%;
  height: 100%;
  display: block;
  object-fit: contain;
}

/* Right Form Section */
.form-section {
  flex: 0 0 48%;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  padding: 112px 50px 100px 30px;
  box-sizing: border-box;
  position: relative;
}

.form-panel {
  width: 100%;
  max-width: 480px;
  margin-bottom: 60px;
  position: relative;
  z-index: 2;
}

.header-logo {
  position: fixed;
  top: 32px;
  left: 50px;
  z-index: 100;
  cursor: pointer;

  .logo-image {
    width: 120px;
    height: auto;
  }
}

.header-links {
  position: fixed;
  top: 28px;
  right: 28px;
  display: flex;
  align-items: center;
  gap: 10px;
  z-index: 100;
}

.header-link {
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 9px 15px;
  border-radius: var(--gy-radius-pill);
  background: color-mix(in oklab, var(--gy-text-inverse) 20%, transparent);
  border: 1px solid color-mix(in oklab, var(--gy-text-inverse) 25%, transparent);
  color: var(--gy-text-inverse);
  text-decoration: none;
  font-size: var(--gy-text-md);
  font-weight: 600;
  font-family: var(--gy-font-sans);
  letter-spacing: 0.2px;
  cursor: pointer;
  position: relative;

  svg {
    flex-shrink: 0;
  }

  .link-text {
    line-height: 1;
  }

  &:hover {
    background: color-mix(in oklab, var(--gy-text-inverse) 30%, transparent);
    border-color: color-mix(in oklab, var(--gy-text-inverse) 40%, transparent);
    color: var(--gy-text-inverse);
  }
}

.language-switch {
  position: relative;

  > button {
    background: color-mix(in oklab, var(--gy-text-inverse) 20%, transparent);
    border: 1px solid color-mix(in oklab, var(--gy-text-inverse) 25%, transparent);
    color: var(--gy-text-inverse);

    .lang-flag-icon {
      font-size: var(--gy-text-xl);
      line-height: 1;
      flex-shrink: 0;
    }

    &:hover {
      background: color-mix(in oklab, var(--gy-text-inverse) 30%, transparent);
      border-color: color-mix(in oklab, var(--gy-text-inverse) 40%, transparent);
    }

    svg:last-child {
      margin-left: 2px;
      flex-shrink: 0;
    }
  }
}

.language-dropdown {
  position: absolute;
  top: calc(100% + 8px);
  right: 0;
  min-width: 160px;
  background: var(--gy-surface-overlay);
  border: 1px solid var(--gy-border-subtle);
  border-radius: var(--gy-radius-md);
  box-shadow: var(--gy-shadow-md);
  overflow: hidden;
  z-index: var(--gy-z-dropdown);
}

.language-option {
  display: flex;
  align-items: center;
  width: 100%;
  border: none;
  background: transparent;
  text-align: left;
  gap: 10px;
  padding: 10px 14px;
  cursor: pointer;
  font-size: var(--gy-text-md);
  font-family: var(--gy-font-sans);
  color: var(--gy-text-primary);

  .lang-flag {
    font-size: var(--gy-text-xl);
    flex-shrink: 0;
  }

  .lang-label {
    flex: 1;
  }

  .check-icon {
    color: var(--gy-success);
    font-weight: 700;
    font-size: var(--gy-text-base);
    flex-shrink: 0;
  }

  &:hover {
    background: var(--gy-surface-subtle);
  }

  &.active {
    background: var(--gy-success-soft);
    color: var(--gy-brand-active);
  }
}

.form-card {
  background: var(--gy-surface-overlay);
  border-radius: var(--gy-radius-xl);
  padding: 40px;
  box-shadow: var(--gy-shadow-lg);
  box-sizing: border-box;
  border: none;
  width: 100%;
}

/* Share-link invitation banner. Sits above the register form when the
 * user arrived via /register?token=xxx; gives them confirmation of who
 * invited them before they fill anything in. Subtle, neutral card —
 * the page background is heavily brand-coloured already, so a loud
 * tinted banner clashes; we lean on the form's own surface tokens. */
.invite-banner {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 12px 14px;
  margin-bottom: 20px;
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container-hover);
  border: 1px solid var(--td-component-stroke);
  color: var(--td-text-color-primary);
}

.invite-banner__icon {
  margin-top: 2px;
  font-size: var(--app-text-2xl);
  flex-shrink: 0;
  color: var(--td-text-color-secondary);
}

.invite-banner__text {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.invite-banner__title {
  font-size: var(--app-text-base);
  font-weight: 600;
  line-height: 1.4;
  color: var(--td-text-color-primary);
}

.invite-banner__hint {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  line-height: 1.5;
}

.invite-banner--error {
  background: var(--td-error-color-1);
  border-color: var(--td-error-color-3);
  color: var(--td-error-color);
  font-size: var(--app-text-md);
}

.form-header {
  text-align: center;
  margin-bottom: 32px;
}

.form-title {
  font-size: var(--app-text-4xl);
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 0 0 6px 0;
  font-family: var(--app-font-family);
}

.form-welcome {
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
  margin: 0;
  font-family: var(--app-font-family);
}

.form-hint {
  margin: 10px 0 0;
  padding: 8px 12px;
  border-radius: var(--gy-radius-md);
  background: var(--gy-success-soft);
  color: var(--gy-brand-active);
  font-size: var(--gy-text-sm);
  line-height: 1.5;
  font-family: var(--gy-font-sans);
}

/* 注册入口：从底部小字链接升级为带分隔线的醒目次级按钮，
   让首次访客一眼就能找到「创建账户」。 */
.register-cta {
  margin-top: 8px;

  &__divider {
    position: relative;
    text-align: center;
    margin: 4px 0 14px;
    color: var(--gy-text-secondary);
    font-size: var(--gy-text-md);
    font-family: var(--gy-font-sans);

    span {
      position: relative;
      z-index: 1;
      padding: 0 12px;
      background: var(--gy-surface-overlay);
    }

    &::before {
      content: '';
      position: absolute;
      left: 0;
      right: 0;
      top: 50%;
      border-top: 1px solid var(--td-component-stroke);
    }
  }

  &__button {
    height: 46px;
    border-radius: var(--app-radius-md);
    font-size: var(--app-text-lg);
    font-weight: 500;
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);

    &:hover {
      border-color: var(--td-brand-color-active);
      color: var(--td-brand-color-active);
      background: var(--td-success-color-light);
    }
  }
}

.form-subtitle {
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
  margin: 0;
  font-family: var(--app-font-family);
}

.form-content {
  :deep(.t-form-item__label) {
    font-size: var(--app-text-base);
    color: var(--td-text-color-primary);
    font-weight: 500;
    margin-bottom: 8px;
    font-family: var(--app-font-family);
    display: block;
    text-align: left;
  }

  :deep(.t-input) {
    border: 1px solid var(--gy-border-subtle);
    border-radius: var(--gy-radius-md);
    background: var(--gy-surface);
    transition: border-color var(--gy-motion-base) ease, box-shadow var(--gy-motion-base) ease;

    &:focus-within {
      border-color: var(--gy-brand);
      box-shadow: 0 0 0 3px var(--gy-brand-tint);
    }

    &:hover {
      border-color: var(--gy-brand);
    }

    .t-input__inner {
      border: none !important;
      box-shadow: none !important;
      outline: none !important;
      background: transparent;
      font-size: var(--app-text-lg);
      font-family: var(--app-font-family);

      &:focus {
        border: none !important;
        box-shadow: none !important;
        outline: none !important;
      }
    }

    .t-input__wrap {
      border: none !important;
      box-shadow: none !important;
    }
  }

  :deep(.t-form-item) {
    margin-bottom: 18px;

    &:last-child {
      margin-bottom: 0;
    }
  }

  :deep(.t-form-item__control) {
    width: 100%;
  }
}

.submit-button {
  height: 46px;
  border-radius: var(--app-radius-md);
  font-size: var(--app-text-xl);
  font-weight: 500;
  font-family: var(--app-font-family);
  margin: 20px 0 16px 0;
}

.oidc-divider {
  position: relative;
  margin: 4px 0 6px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);

  span {
    position: relative;
    z-index: 1;
    padding: 0 12px;
    background: var(--gy-surface-overlay);
  }

  &::before {
    content: '';
    position: absolute;
    left: 0;
    right: 0;
    top: 50%;
    border-top: 1px solid var(--td-component-stroke);
  }
}

.oidc-button {
  height: 46px;
  border-radius: var(--app-radius-md);
  font-size: var(--app-text-lg);
  font-weight: 500;
}

.form-footer {
  text-align: center;
  font-size: var(--app-text-base);
  color: var(--td-text-color-secondary);
  font-family: var(--app-font-family);
  margin-top: 16px;
  padding-bottom: 16px;
  border-bottom: 1px solid var(--td-component-stroke);

  .link-button {
    color: var(--gy-brand);
    text-decoration: none;
    margin-left: 4px;
    font-weight: 500;
    transition: color var(--gy-motion-base) ease;

    &:hover {
      color: var(--td-brand-color);
      text-decoration: underline;
    }
  }
}

.login-form-footer {
  border-bottom: none;
  padding-bottom: 8px;
  margin-top: 12px;
}

.login-features {
  margin-top: 20px;
  padding: 0;

  .feature-item {
    display: flex;
    align-items: center;
    margin-bottom: 12px;
    font-size: var(--app-text-md);
    color: var(--td-text-color-secondary);
    font-family: var(--app-font-family);

    &:last-child {
      margin-bottom: 0;
    }

    .feature-icon {
      width: 20px;
      height: 20px;
      border-radius: 50%;
      background: var(--td-success-color-light);
      color: var(--td-brand-color-active);
      display: flex;
      align-items: center;
      justify-content: center;
      font-size: var(--app-text-sm);
      font-weight: 700;
      margin-right: 10px;
      flex-shrink: 0;
    }

    .feature-text {
      line-height: 1.4;
    }
  }
}

/* Responsive Design */
@media (max-width: 1024px) {
  .knowledge-node:nth-of-type(n + 13) {
    display: none;
  }

  .connection-line:nth-of-type(n + 13) {
    display: none;
  }

  .showcase-subtitle {
    font-size: var(--app-text-2xl);
  }

  .header-logo {
    top: 26px;
    left: 40px;

    .logo-image {
      width: 100px;
    }
  }

  .header-links {
    top: 22px;
    right: 22px;
    gap: 8px;

    .link-text {
      display: none;
    }

    .header-link {
      padding: 10px;
      gap: 0;
    }
  }
}

@media (max-width: 768px) {
  .login-layout {
    flex-direction: column;
    padding-top: 64px;
    box-sizing: border-box;
  }

  .knowledge-node:nth-of-type(n + 9) {
    display: none;
  }

  .connection-line:nth-of-type(n + 9) {
    display: none;
  }

  .showcase-section {
    flex: 0 0 auto;
    min-height: 50vh;
    padding: 40px 24px;
  }

  .showcase-content {
    max-width: 100%;
  }

  .header-logo {
    top: 22px;
    left: 30px;

    .logo-image {
      width: 80px;
    }
  }

  .showcase-subtitle {
    font-size: var(--app-text-xl);
    margin-bottom: 24px;
  }

  .feature-tags {
    margin-bottom: 24px;
  }

  .carousel-container {
    margin-top: 24px;
  }

  .form-section {
    order: -1;
    flex: 0 0 auto;
    padding: 24px;
  }

  .header-links {
    top: 18px;
    right: 18px;
    gap: 8px;

    .link-text {
      display: inline;
    }

    .header-link {
      padding: 8px 12px;
      font-size: var(--app-text-sm);
    }
  }

  .form-card {
    padding: 32px 24px;
  }

  .form-title {
    font-size: var(--gy-text-3xl);
  }
}

@media (max-width: 480px) {
  .animated-bg {
    display: none;
  }

  .showcase-section {
    padding: 32px 20px;
  }

  .header-logo {
    top: 18px;
    left: 20px;

    .logo-image {
      width: 70px;
    }
  }

  .showcase-subtitle {
    font-size: var(--app-text-base);
  }

  .tag {
    font-size: var(--app-text-sm);
    padding: 6px 16px;
  }

  .form-section {
    padding: 20px;
  }

  .header-links {
    top: 14px;
    right: 14px;
    gap: 6px;
    flex-wrap: wrap;

    .link-text {
      display: none;
    }

    .header-link {
      padding: 7px 10px;
      font-size: var(--app-text-xs);
    }
  }

  .form-card {
    padding: 28px 20px;
  }

  .form-header {
    margin-bottom: 24px;
  }
}

@media (prefers-reduced-motion: reduce) {

  .knowledge-node,
  .connection-line {
    animation: none !important;
    transition: none !important;
  }

  .animated-bg {
    display: none;
  }
}
</style>

<style lang="less">
html[theme-mode="dark"] {
  .login-layout {
    background: linear-gradient(
      225deg,
      var(--gy-surface-page) 0%,
      var(--td-brand-color-2) 25%,
      var(--td-brand-color-3) 50%,
      var(--td-brand-color-4) 75%,
      var(--td-brand-color-5) 100%
    );
  }

  .knowledge-node {
    background: color-mix(in oklab, var(--gy-text-inverse) 10%, transparent);
    border-color: color-mix(in oklab, var(--gy-text-inverse) 20%, transparent);
    box-shadow: var(--gy-shadow-sm);
  }

  .connection-line {
    stroke: color-mix(in oklab, var(--gy-text-inverse) 25%, transparent);
  }

  .header-logo .logo-image {
    filter: invert(1) hue-rotate(180deg) brightness(1.1);
  }

  .header-link {
    background: color-mix(in oklab, var(--gy-text-inverse) 12%, transparent);
    border-color: color-mix(in oklab, var(--gy-text-inverse) 15%, transparent);

    &:hover {
      background: color-mix(in oklab, var(--gy-text-inverse) 20%, transparent);
    }
  }

  .language-switch > button {
    background: color-mix(in oklab, var(--gy-text-inverse) 12%, transparent);
    border-color: color-mix(in oklab, var(--gy-text-inverse) 15%, transparent);

    &:hover {
      background: color-mix(in oklab, var(--gy-text-inverse) 20%, transparent);
    }
  }

  .tag {
    background: color-mix(in oklab, var(--gy-text-inverse) 12%, transparent);
  }

  .form-content .t-input {
    background: var(--gy-surface-page);
    border-color: var(--gy-border-overlay);

    &:hover,
    &:focus-within {
      border-color: var(--gy-brand);
    }
  }

  .screenshot-swiper .swiper-pagination-bullet-active {
    background: var(--gy-text-primary);
  }

  .login-features .feature-icon {
    background: var(--gy-brand-soft);
  }
}
</style>
