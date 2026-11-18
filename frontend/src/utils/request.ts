// src/utils/request.ts
import axios, {
  type AxiosProgressEvent,
  type AxiosRequestConfig,
  type InternalAxiosRequestConfig,
} from "axios";
import { generateRandomString, MAX_FILE_SIZE_MB, MAX_SKILL_BUNDLE_SIZE_MB } from "./index";
import i18n from '@/i18n'
import { getApiBaseUrl } from './api-base';
import { isSkillBundleUploadUrl } from './uploadLimit';
import { isTimeoutError, uploadTimeoutMs } from './requestTimeouts';
import {
  AuthSessionChangedError,
  assertAuthRequestContext,
  captureAuthRequestContext,
  forceReloginRedirect,
  isEmbedPage,
  refreshAccessTokenShared,
  type AuthRequestContext,
} from './authRefresh';

export { forceReloginRedirect, refreshAccessTokenShared };

const t = (key: string) => i18n.global.t(key)

// API基础URL
const BASE_URL = getApiBaseUrl();

/**
 * Response payload augmented with the HTTP status code.
 *
 * `$httpStatus` lets callers distinguish outcomes that share a success shape.
 * Defined as a non-enumerable property, so it stays invisible to object spread,
 * JSON.stringify and Object.keys and never leaks into downstream payloads.
 *
 * Guaranteed only for JSON responses (objects/arrays). Blob, string and SSE
 * stream responses do not carry it at runtime, so only read `$httpStatus`
 * when the payload is known to be an object.
 */
export type WithStatus<T> = T & {
  /** HTTP status code of the response. Non-enumerable. See {@link WithStatus}. */
  readonly $httpStatus: number
};

const HTTP_STATUS_KEY = '$httpStatus';

type AuthenticatedRequestConfig = InternalAxiosRequestConfig & {
  _authContext?: AuthRequestContext;
  _retry?: boolean;
};

function assertNotCancelled(config: AuthenticatedRequestConfig): void {
  config.cancelToken?.throwIfRequested();
  if (config.signal?.aborted) throw new axios.CanceledError(undefined, config);
}

/** Cancelling one caller must not cancel a refresh shared by other requests. */
function waitForRefresh(token: Promise<string>, config: AuthenticatedRequestConfig): Promise<string> {
  const signal = config.signal;
  if (!signal) return token;
  return new Promise((resolve, reject) => {
    const onAbort = () => {
      signal.removeEventListener?.('abort', onAbort);
      reject(new axios.CanceledError(undefined, config));
    };
    signal.addEventListener?.('abort', onAbort, { once: true });
    if (signal.aborted) onAbort();
    token.then(
      value => { signal.removeEventListener?.('abort', onAbort); resolve(value); },
      error => { signal.removeEventListener?.('abort', onAbort); reject(error); },
    );
  });
}

/**
 * Attach the non-enumerable `$httpStatus` property to a response payload
 * in place and return it. Primitives pass through untouched.
 * See {@link WithStatus} for where the property is guaranteed.
 */
function withHttpStatus<T>(data: T, status: number): T {
  if (data !== null && typeof data === 'object') {
    Object.defineProperty(data, HTTP_STATUS_KEY, {
      value: status,
      enumerable: false,
      configurable: true,
      writable: false,
    });
  }
  return data;
}

// 创建Axios实例
const instance = axios.create({
  baseURL: BASE_URL, // 使用配置的API基础URL
  timeout: 30000, // 请求超时时间
  headers: {
    "Content-Type": "application/json",
    "X-Request-ID": `${generateRandomString(12)}`,
  },
});

// 获取当前用户语言（用于 Accept-Language header）
export function getCurrentLanguage(): string {
  return i18n.global.locale?.value || localStorage.getItem('locale') || 'zh-CN'
}

function resolveTrustedApiOrigin(): string {
  const configured = String(BASE_URL || '').trim();
  const winOrigin = typeof window !== 'undefined' && window.location?.origin ? window.location.origin : '';
  if (configured && /^[a-zA-Z][a-zA-Z\d+\-.]*:/.test(configured)) {
    try {
      return new URL(configured, winOrigin || undefined).origin;
    } catch {
      return winOrigin;
    }
  }
  return winOrigin;
}

function isTrustedApiRequest(config?: { url?: string; baseURL?: string }): boolean {
  const rawUrl = String(config?.url || '').trim();
  if (!rawUrl) return true;
  if (rawUrl.startsWith('//') || rawUrl.startsWith('\\\\')) return false;
  if (/^[a-zA-Z][a-zA-Z\d+\-.]*:/.test(rawUrl)) {
    try {
      const parsed = new URL(rawUrl);
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return false;
      const trustedOrigin = resolveTrustedApiOrigin();
      return Boolean(trustedOrigin && parsed.origin === trustedOrigin);
    } catch {
      return false;
    }
  }
  return true;
}

instance.interceptors.request.use(
  (config) => {
    const authenticatedConfig = config as AuthenticatedRequestConfig;
    assertNotCancelled(authenticatedConfig);
    const existingAuth = config.headers?.Authorization ?? config.headers?.authorization;
    const isEmbedAuth = typeof existingAuth === 'string' && existingAuth.startsWith('Embed ');
    const isEmbedPath = typeof config.url === 'string' && config.url.includes('/api/v1/embed/');
    const isTrusted = isTrustedApiRequest(config);

    // 嵌入渠道使用 Embed token，跨域请求决不附带本地 JWT 或 X-Tenant-ID
    if (isTrusted && !isEmbedAuth && !isEmbedPath) {
      if (authenticatedConfig._authContext) {
        assertAuthRequestContext(authenticatedConfig._authContext);
      } else {
        authenticatedConfig._authContext = captureAuthRequestContext();
      }
      const token = localStorage.getItem('genyee_token');
      if (token) {
        config.headers["Authorization"] = `Bearer ${token}`;
      }
    }
    
    // 添加用户语言偏好
    config.headers["Accept-Language"] = getCurrentLanguage();
    
    if (isTrusted && !isEmbedAuth && !isEmbedPath) {
      const selectedTenantId = localStorage.getItem('genyee_selected_tenant_id');
      if (selectedTenantId) {
        config.headers["X-Tenant-ID"] = selectedTenantId;
      }
    }
    
    config.headers["X-Request-ID"] = `${generateRandomString(12)}`;
    return config;
  },
  (error) => {
    return Promise.reject(error);
  }
);

// Share-link endpoints (/auth/invitations/lookup, /auth/register-by-invite)
// are reachable by anonymous users opening an invite link. A 401 from these
// must surface to the page (e.g. expired token), not trigger the
// refresh-then-redirect-to-login flow (issue #1617). '/auth/register' already
// covers '/auth/register-by-invite' via substring match.
const PUBLIC_AUTH_PATHS = ['/auth/auto-setup', '/auth/login', '/auth/register', '/auth/oidc/', '/auth/invitations/lookup', '/api/v1/embed/'];

function isPublicAuthRequest(url?: string): boolean {
  if (!url) return false;
  return PUBLIC_AUTH_PATHS.some(p => url.includes(p));
}

export interface DefaultApiPayload {
  success?: boolean;
  data?: unknown;
  total?: number;
  page?: number;
  page_size?: number;
  message?: string;
  code?: string | number;
  error?: { code?: string | number; message?: string; details?: unknown } | string;
  [key: string]: unknown;
}

interface AxiosErrorLike {
  config?: AuthenticatedRequestConfig;
  response?: {
    status: number;
    data: unknown;
  };
  message?: string;
}

function extractErrorMessage(data: unknown): string | undefined {
  if (typeof data === 'string') return data;
  if (!data || typeof data !== 'object') return undefined;
  const obj = data as { error?: unknown; message?: unknown };
  if (typeof obj.error === 'string') return obj.error;
  if (obj.error && typeof obj.error === 'object') {
    const nested = (obj.error as { message?: unknown }).message;
    if (typeof nested === 'string') return nested;
  }
  return typeof obj.message === 'string' ? obj.message : undefined;
}

instance.interceptors.response.use(
  (response) => {
    const { status, data } = response;
    if (status >= 200 && status < 300) {
      return withHttpStatus(data, status);
    } else {
      return Promise.reject(withHttpStatus(data, status));
    }
  },
  async (rawError: unknown) => {
    const error = (rawError && typeof rawError === 'object' ? rawError : {}) as AxiosErrorLike;
    const originalRequest = error.config;

    if (axios.isCancel(rawError) || rawError instanceof AuthSessionChangedError) {
      return Promise.reject(rawError);
    }

    if (!error.response) {
      return Promise.reject({
        message: t(isTimeoutError(rawError) ? 'error.requestTimeout' : 'error.networkError'),
      });
    }

    if (typeof Blob !== 'undefined' && error.response.data instanceof Blob) {
      try {
        const text = (await error.response.data.text()).trim();
        if (text.startsWith('{') || text.startsWith('[') || error.response.data.type.includes('json')) {
          error.response.data = JSON.parse(text) as unknown;
        }
      } catch {
        // Invalid JSON falls through to standard error handling.
      }
    }

    if ((error.response.status === 401 || error.response.status === 403) && isPublicAuthRequest(originalRequest?.url)) {
      const { status, data } = error.response;
      const msg = extractErrorMessage(data);
      return Promise.reject(withHttpStatus({ status, message: msg || t('error.invalidCredentials') }, status));
    }

    if (error.response.status === 401 && isEmbedPage()) {
      const { status, data } = error.response;
      const msg = extractErrorMessage(data);
      return Promise.reject(withHttpStatus({ status, message: msg || t('error.invalidCredentials') }, status));
    }

    const isUnreplayableStream =
      typeof ReadableStream !== 'undefined' && originalRequest?.data instanceof ReadableStream;
    if (
      error.response.status === 401 &&
      originalRequest &&
      isTrustedApiRequest(originalRequest) &&
      !isUnreplayableStream &&
      !originalRequest._retry &&
      !originalRequest.url?.includes('/auth/refresh')
    ) {
      originalRequest._retry = true;
      try {
        assertNotCancelled(originalRequest);
        const context = originalRequest._authContext;
        if (!context) throw new AuthSessionChangedError();
        const currentToken = assertAuthRequestContext(context);
        const token = currentToken && originalRequest.headers.Authorization !== `Bearer ${currentToken}`
          ? currentToken
          : await waitForRefresh(refreshAccessTokenShared({
            messages: {
              pleaseRelogin: t('error.pleaseRelogin'),
              tokenRefreshFailed: t('error.tokenRefreshFailed'),
            },
          }), originalRequest);
        assertNotCancelled(originalRequest);
        assertAuthRequestContext(context);
        originalRequest.headers['Authorization'] = 'Bearer ' + token;
        return instance(originalRequest);
      } catch (refreshError) {
        return Promise.reject(refreshError);
      }
    }

    const ERR_ENTITY_TOO_LARGE = 413;
    if (error.response.status === ERR_ENTITY_TOO_LARGE) {
      const skillUpload = isSkillBundleUploadUrl(error.config?.url);
      return Promise.reject(withHttpStatus({
        status: ERR_ENTITY_TOO_LARGE,
        message: skillUpload
          ? i18n.global.t('settings.sandbox.skillBundleTooLarge', { size: MAX_SKILL_BUNDLE_SIZE_MB })
          : i18n.global.t('error.fileSizeExceeded', { size: MAX_FILE_SIZE_MB }),
        success: false,
      }, ERR_ENTITY_TOO_LARGE));
    }

    const { status, data } = error.response;
    const errorMessage = extractErrorMessage(data);
    return Promise.reject(withHttpStatus({
      status,
      message: errorMessage,
      ...(data && typeof data === 'object' ? (data as Record<string, unknown>) : {}),
    }, status));
  }
);

export function get<T = DefaultApiPayload>(
  url: string,
  config?: AxiosRequestConfig,
): Promise<WithStatus<T>> {
  return instance.get<T>(url, config) as unknown as Promise<WithStatus<T>>;
}

export async function getDown(url: string): Promise<Blob> {
  const res = await instance.get<Blob>(url, {
    responseType: "blob",
  }) as unknown as Blob;
  return res;
}

export function postUpload<T = DefaultApiPayload>(
  url: string,
  data: unknown = {},
  onUploadProgress?: (progressEvent: AxiosProgressEvent) => void,
  config: AxiosRequestConfig = {},
): Promise<WithStatus<T>> {
  return instance.post(url, data, {
    timeout: uploadTimeoutMs(data),
    ...config,
    headers: {
      "Content-Type": "multipart/form-data",
      "X-Request-ID": `${generateRandomString(12)}`,
      ...(config.headers || {}),
    },
    onUploadProgress: onUploadProgress || config.onUploadProgress,
  }) as unknown as Promise<WithStatus<T>>;
}

export function postChat<T = string>(url: string, data: unknown = {}): Promise<T> {
  // SSE stream: body is a string, so no `$httpStatus` is attached (see WithStatus).
  return instance.post(url, data, {
    headers: {
      "Content-Type": "text/event-stream;charset=utf-8",
      "X-Request-ID": `${generateRandomString(12)}`,
    },
  }) as unknown as Promise<T>;
}

export function post<T = DefaultApiPayload>(
  url: string,
  data: unknown = {},
  config?: AxiosRequestConfig,
): Promise<WithStatus<T>> {
  return instance.post<T>(url, data, config) as unknown as Promise<WithStatus<T>>;
}

export function put<T = DefaultApiPayload>(
  url: string,
  data: unknown = {},
  config?: AxiosRequestConfig,
): Promise<WithStatus<T>> {
  return instance.put<T>(url, data, config) as unknown as Promise<WithStatus<T>>;
}

export function patch<T = DefaultApiPayload>(
  url: string,
  data: unknown = {},
  config?: AxiosRequestConfig,
): Promise<WithStatus<T>> {
  return instance.patch<T>(url, data, config) as unknown as Promise<WithStatus<T>>;
}

export function del<T = DefaultApiPayload>(
  url: string,
  data?: unknown,
): Promise<WithStatus<T>> {
  return instance.delete<T>(url, { data }) as unknown as Promise<WithStatus<T>>;
}
