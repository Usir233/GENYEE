// src/utils/request.js
import axios, { type InternalAxiosRequestConfig } from "axios";
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

instance.interceptors.response.use(
  (response) => {
    // 根据业务状态码处理逻辑
    const { status, data } = response;
    if (status >= 200 && status < 300) {
      return withHttpStatus(data, status);
    } else {
      return Promise.reject(withHttpStatus(data, status));
    }
  },
  async (error: any) => {
    const originalRequest = error.config as AuthenticatedRequestConfig | undefined;

    if (axios.isCancel(error) || error instanceof AuthSessionChangedError) {
      return Promise.reject(error);
    }
    
    if (!error.response) {
      // A timeout and an unreachable server both arrive without a response, but
      // telling someone whose upload timed out to "check your connection" sends
      // them after the wrong problem.
      return Promise.reject({
        message: t(isTimeoutError(error) ? 'error.requestTimeout' : 'error.networkError'),
      });
    }

    // 文件下载失败时服务端仍返回 JSON；先还原错误信息，避免被 Blob 隐藏。
    // 不依赖 Content-Type：网关可能把错误改成 text/plain 或空类型。
    if (typeof Blob !== 'undefined' && error.response.data instanceof Blob) {
      try {
        const text = (await error.response.data.text()).trim();
        if (text.startsWith('{') || text.startsWith('[') || error.response.data.type.includes('json')) {
          error.response.data = JSON.parse(text);
        }
      } catch {
        // 非法 JSON 继续使用原有错误处理。
      }
    }
    
    // 公开接口（auto-setup / login / register / oidc）的 401 不走 refresh 逻辑，直接返回错误
    if ((error.response.status === 401 || error.response.status === 403) && isPublicAuthRequest(originalRequest?.url)) {
      const { status, data } = error.response;
      const msg = typeof data === 'object'
        ? (typeof data?.error === 'string' ? data.error : (data?.error?.message || data?.message))
        : data;
      return Promise.reject(withHttpStatus({ status, message: msg || t('error.invalidCredentials') }, status));
    }

    // Embed 调试页/挂件：无 JWT 时直接拒绝，勿走 refresh → /login
    if (error.response.status === 401 && isEmbedPage()) {
      const { status, data } = error.response;
      const msg = typeof data === 'object'
        ? (typeof data?.error === 'string' ? data.error : (data?.error?.message || data?.message))
        : data;
      return Promise.reject(withHttpStatus({ status, message: msg || t('error.invalidCredentials') }, status));
    }

    // 如果是401错误且不是刷新token的请求，尝试刷新token（仅限同源受信任 API 且非单次消费流请求体）
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
        // Another concurrent request may already have rotated this session.
        // A late 401 should reuse that token, not rotate the new refresh again.
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
        // Refresh failures handle relogin themselves. Superseded/cancelled
        // operations leave the currently active account and tenant untouched.
        return Promise.reject(refreshError);
      }
    }
    
    // 处理 Nginx 413 Request Entity Too Large
    const ERR_ENTITY_TOO_LARGE = 413;
    if (error.response.status === ERR_ENTITY_TOO_LARGE) {
      const skillUpload = isSkillBundleUploadUrl(error.config?.url)
      return Promise.reject(withHttpStatus({
        status: ERR_ENTITY_TOO_LARGE,
        message: skillUpload
          ? i18n.global.t('settings.sandbox.skillBundleTooLarge', { size: MAX_SKILL_BUNDLE_SIZE_MB })
          : i18n.global.t('error.fileSizeExceeded', { size: MAX_FILE_SIZE_MB }),
        success: false
      }, ERR_ENTITY_TOO_LARGE));
    }

    const { status, data } = error.response;
    // 将HTTP状态码一并抛出，方便上层判断401等场景
    // 后端返回格式: { success: false, error: { code, message, details } }
    // 提取 error.message 作为顶层 message，方便前端使用 error?.message 获取
    let errorMessage: string | undefined;
    if (typeof data === 'object') {
      if (typeof data?.error === 'string') {
        errorMessage = data.error;
      } else if (data?.error?.message) {
        errorMessage = data.error.message;
      } else {
        errorMessage = data?.message;
      }
    } else if (typeof data === 'string') {
      errorMessage = data;
    }
    return Promise.reject(withHttpStatus({
      status,
      message: errorMessage,
      ...(typeof data === 'object' ? data : {}) 
    }, status));
  }
);

export function get<T = any>(url: string, config?: any): Promise<WithStatus<T>> {
  return instance.get<T>(url, config) as unknown as Promise<WithStatus<T>>;
}

export async function getDown(url: string): Promise<Blob> {
  const res = await instance.get<Blob>(url, {
    responseType: "blob",
  }) as unknown as Blob;
  return res
}

export function postUpload(
  url: string,
  data = {},
  onUploadProgress?: (progressEvent: any) => void,
  config: any = {},
): Promise<WithStatus<any>> {
  return instance.post(url, data, {
    // Uploads are bounded by transfer time, not by the 30s default that suits
    // JSON calls. Derive the budget from the payload so a deployment raising
    // MAX_FILE_SIZE_MB doesn't silently abort its own uploads; an explicit
    // `config.timeout` still wins.
    timeout: uploadTimeoutMs(data),
    ...config,
    headers: {
      "Content-Type": "multipart/form-data",
      "X-Request-ID": `${generateRandomString(12)}`,
      ...(config.headers || {}),
    },
    onUploadProgress: onUploadProgress || config.onUploadProgress,
  }) as unknown as Promise<any>;
}

export function postChat<T = any>(url: string, data = {}): Promise<T> {
  // SSE stream: body is a string, so no `$httpStatus` is attached (see WithStatus).
  return instance.post(url, data, {
    headers: {
      "Content-Type": "text/event-stream;charset=utf-8",
      "X-Request-ID": `${generateRandomString(12)}`,
    },
  }) as unknown as Promise<T>;
}

export function post<T = any>(url: string, data = {}, config?: any): Promise<WithStatus<T>> {
  return instance.post<T>(url, data, config) as unknown as Promise<WithStatus<T>>;
}

export function put<T = any>(url: string, data = {}, config?: any): Promise<WithStatus<T>> {
  return instance.put<T>(url, data, config) as unknown as Promise<WithStatus<T>>;
}

export function patch<T = any>(url: string, data = {}, config?: any): Promise<WithStatus<T>> {
  return instance.patch<T>(url, data, config) as unknown as Promise<WithStatus<T>>;
}

export function del<T = any>(url: string, data?: any): Promise<WithStatus<T>> {
  return instance.delete<T>(url, { data }) as unknown as Promise<WithStatus<T>>;
}
