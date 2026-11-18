import { onMounted, onUnmounted, ref, type Ref } from 'vue';

export interface ResizableDrawerOptions {
  storageKey: string;
  defaultWidth: number;
  minWidth: number;
  maxPixelWidth: number;
  viewportRatio: number;
}

export interface ResizableDrawerController {
  width: Ref<number>;
  resizing: Ref<boolean>;
  clampWidth: (candidate: number) => number;
  loadWidth: () => void;
  onResizeStart: (e: MouseEvent) => void;
  cleanupResize: () => void;
}

export function useResizableDrawerWidth(options: ResizableDrawerOptions): ResizableDrawerController {
  const { storageKey, defaultWidth, minWidth, maxPixelWidth, viewportRatio } = options;
  const width = ref(defaultWidth);
  const resizing = ref(false);

  let resizeStartX = 0;
  let resizeStartWidth = 0;

  const getMaxWidth = (): number => {
    const viewportWidth = typeof window !== 'undefined' ? window.innerWidth : defaultWidth;
    return Math.min(maxPixelWidth, Math.max(minWidth, Math.floor(viewportWidth * viewportRatio)));
  };

  const clampWidth = (candidate: number): number =>
    Math.max(minWidth, Math.min(getMaxWidth(), candidate));

  const loadWidth = (): void => {
    try {
      const raw = localStorage.getItem(storageKey);
      const parsed = raw ? Number.parseInt(raw, 10) : Number.NaN;
      if (!Number.isNaN(parsed)) {
        width.value = clampWidth(parsed);
      }
    } catch {
      /* ignore quota / private mode */
    }
  };

  const onResizeMove = (e: MouseEvent): void => {
    const delta = resizeStartX - e.clientX;
    width.value = clampWidth(resizeStartWidth + delta);
  };

  const onResizeEnd = (): void => {
    document.removeEventListener('mousemove', onResizeMove);
    document.removeEventListener('mouseup', onResizeEnd);
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    resizing.value = false;
    try {
      localStorage.setItem(storageKey, String(width.value));
    } catch {
      /* ignore */
    }
  };

  const onResizeStart = (e: MouseEvent): void => {
    resizing.value = true;
    resizeStartX = e.clientX;
    resizeStartWidth = width.value;
    document.addEventListener('mousemove', onResizeMove);
    document.addEventListener('mouseup', onResizeEnd);
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  };

  const cleanupResize = (): void => {
    if (typeof document !== 'undefined') {
      document.removeEventListener('mousemove', onResizeMove);
      document.removeEventListener('mouseup', onResizeEnd);
      if (document.body?.style) {
        document.body.style.cursor = '';
        document.body.style.userSelect = '';
      }
    }
    resizing.value = false;
  };

  return {
    width,
    resizing,
    clampWidth,
    loadWidth,
    onResizeStart,
    cleanupResize,
  };
}

export function useDrawerSplitResize() {
  const traceDrawer = useResizableDrawerWidth({
    storageKey: 'genyee-trace-drawer-width',
    defaultWidth: 820,
    minWidth: 560,
    maxPixelWidth: 1400,
    viewportRatio: 0.92,
  });

  const mainDrawer = useResizableDrawerWidth({
    storageKey: 'genyee-doc-drawer-width',
    defaultWidth: 654,
    minWidth: 480,
    maxPixelWidth: 1600,
    viewportRatio: 0.95,
  });

  const syncViewportBounds = (): void => {
    traceDrawer.width.value = traceDrawer.clampWidth(traceDrawer.width.value);
    mainDrawer.width.value = mainDrawer.clampWidth(mainDrawer.width.value);
  };

  let viewportObserver: ResizeObserver | null = null;
  let fallbackWindowListenerAttached = false;

  onMounted(() => {
    traceDrawer.loadWidth();
    mainDrawer.loadWidth();
    if (typeof ResizeObserver !== 'undefined' && typeof document !== 'undefined' && document.documentElement) {
      viewportObserver = new ResizeObserver(() => {
        syncViewportBounds();
      });
      viewportObserver.observe(document.documentElement);
    } else if (typeof window !== 'undefined') {
      window.addEventListener('resize', syncViewportBounds, { passive: true });
      fallbackWindowListenerAttached = true;
    }
  });

  onUnmounted(() => {
    if (viewportObserver) {
      viewportObserver.disconnect();
      viewportObserver = null;
    }
    if (fallbackWindowListenerAttached && typeof window !== 'undefined') {
      window.removeEventListener('resize', syncViewportBounds);
      fallbackWindowListenerAttached = false;
    }
    traceDrawer.cleanupResize();
    mainDrawer.cleanupResize();
  });

  return {
    timelineDrawerWidth: traceDrawer.width,
    timelineDrawerResizing: traceDrawer.resizing,
    onTraceDrawerResizeStart: traceDrawer.onResizeStart,
    mainDrawerWidth: mainDrawer.width,
    mainDrawerResizing: mainDrawer.resizing,
    onMainDrawerResizeStart: mainDrawer.onResizeStart,
  };
}
