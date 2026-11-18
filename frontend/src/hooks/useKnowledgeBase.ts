import { ref, reactive } from "vue";
import { storeToRefs } from "pinia";
import { formatStringDate } from "../utils/index";
import {
  listKnowledgeFiles,
  getKnowledgeDetails,
  getKnowledgeDetailsCon,
  type ListKnowledgeFilesParams,
} from "@/api/knowledge-base/index";
import { knowledgeStore } from "@/stores/knowledge";
import { useRoute } from 'vue-router';
import { useI18n } from 'vue-i18n';

export interface KnowledgeTagRef {
  id: string;
  name: string;
  color?: string;
}

export interface KnowledgeFileRecord {
  id: string;
  file_name?: string;
  title?: string;
  source?: string;
  type?: string;
  channel?: string;
  file_type?: string;
  folder_path?: string;
  description?: string;
  summary_status?: string;
  parse_status?: string;
  error_message?: string;
  custom_metadata?: Record<string, unknown>;
  updated_at?: string | number | Date;
  tags?: KnowledgeTagRef[];
  [key: string]: unknown;
}

interface KnowledgeListResponse {
  data: KnowledgeFileRecord[];
  total: number;
}

interface KnowledgeDetailResponse {
  success?: boolean;
  data?: KnowledgeFileRecord;
}

interface KnowledgeChunksResponse {
  success?: boolean;
  data?: Array<Record<string, unknown>>;
  total?: number;
  message?: string;
  error?: { message?: string };
}

export default function (knowledgeBaseId?: string) {
  const usemenuStore = knowledgeStore();
  const route = useRoute();
  const { t } = useI18n();
  const { cardList, total } = storeToRefs(usemenuStore);
  const moreIndex = ref(-1);
  const details = reactive({
    title: "",
    time: "",
    md: [] as Array<Record<string, unknown>>,
    id: "",
    total: 0,
    type: "",
    source: "",
    channel: "",
    file_type: "",
    description: "",
    summary_status: "",
    parse_status: "",
    error_message: "",
    custom_metadata: {} as Record<string, unknown>,
    chunkLoading: false,
    chunkLoadError: "",
    tags: [] as KnowledgeTagRef[],
  });
  let knowledgeListGeneration = 0;
  let detailRequestGeneration = 0;
  let chunkRequestGeneration = 0;
  let activeKnowledgeId = '';
  const getKnowled = (
    query: ListKnowledgeFilesParams = { page: 1, page_size: 35 },
    kbId?: string,
  ): Promise<void> => {
    const targetKbId = kbId || knowledgeBaseId;
    if (!targetKbId) return Promise.resolve();
    const requestGeneration = query.page === 1 ? ++knowledgeListGeneration : knowledgeListGeneration;

    return (listKnowledgeFiles(targetKbId, query) as Promise<KnowledgeListResponse>)
      .then((result) => {
        if (requestGeneration !== knowledgeListGeneration) return;

        const rawParam = route.params?.kbId;
        const currentRouteKbId = Array.isArray(rawParam) ? rawParam[0] : rawParam;
        if (currentRouteKbId && currentRouteKbId !== targetKbId) return;

        const { data, total: totalResult } = result;
        const cardList_ = (data || []).map((item) => {
          const rawName = item.file_name || item.title || item.source || t('knowledgeBase.untitledDocument');
          const dotIndex = rawName.lastIndexOf('.');
          const displayName = dotIndex > 0 ? rawName.substring(0, dotIndex) : rawName;
          const fileTypeSource = item.file_type || (item.type === 'manual' ? 'MANUAL' : '');
          return {
            ...item,
            original_file_name: item.file_name,
            display_name: displayName,
            file_name: displayName,
            folder_path: item.folder_path || '',
            updated_at: formatStringDate(new Date(item.updated_at ?? Date.now())),
            isMore: false,
            file_type: fileTypeSource ? String(fileTypeSource).toLocaleUpperCase() : '',
          };
        });

        if (query.page === 1) {
          cardList.value = cardList_;
        } else {
          cardList.value.push(...cardList_);
        }
        total.value = totalResult;
      })
      .catch(() => {});
  };
  const openMore = (index: number) => {
    moreIndex.value = index;
  };
  const onVisibleChange = (visible: boolean) => {
    if (!visible) {
      moreIndex.value = -1;
    }
  };
  const getCardDetails = (item: Pick<KnowledgeFileRecord, 'id' | 'tags'>) => {
    const requestGeneration = ++detailRequestGeneration;
    activeKnowledgeId = item.id;
    chunkRequestGeneration++;
    Object.assign(details, {
      title: "",
      time: "",
      md: [],
      id: "",
      type: "",
      source: "",
      channel: "",
      file_type: "",
      description: "",
      summary_status: "",
      parse_status: "",
      error_message: "",
      custom_metadata: {},
      chunkLoadError: "",
      tags: item?.tags ? [...item.tags] : [],
    });
    (getKnowledgeDetails(item.id) as Promise<KnowledgeDetailResponse>)
      .then((result) => {
        if (requestGeneration !== detailRequestGeneration || activeKnowledgeId !== item.id) return;
        if (result.success && result.data) {
          const { data } = result;
          Object.assign(details, {
            title: data.file_name || data.title || data.source || t('knowledgeBase.untitledDocument'),
            time: formatStringDate(new Date(data.updated_at ?? Date.now())),
            id: data.id,
            type: data.type || 'file',
            source: data.source || '',
            channel: data.channel || '',
            file_type: data.file_type || '',
            description: data.description || '',
            summary_status: data.summary_status || '',
            parse_status: data.parse_status || '',
            error_message: data.error_message || '',
            custom_metadata: data.custom_metadata || {},
            tags: data.tags?.length ? data.tags : (item?.tags || []),
          });
        }
      })
      .catch(() => {});
    getfDetails(item.id, 1);
  };

  const getfDetails = (id: string, page: number) => {
    const requestGeneration = ++chunkRequestGeneration;
    details.chunkLoading = true;
    details.chunkLoadError = "";
    (getKnowledgeDetailsCon(id, page) as Promise<KnowledgeChunksResponse>)
      .then((result) => {
        if (requestGeneration !== chunkRequestGeneration || activeKnowledgeId !== id) return;
        if (result.success && result.data) {
          const { data, total: totalResult } = result;
          details.md = data;
          details.total = totalResult ?? 0;
        } else {
          details.chunkLoadError = result?.message || result?.error?.message || t('knowledgeBase.chunkLoadFailed');
        }
      })
      .catch((err: unknown) => {
        if (requestGeneration !== chunkRequestGeneration || activeKnowledgeId !== id) return;
        const msg = err instanceof Error ? err.message : (err as { message?: string } | null)?.message;
        details.chunkLoadError = msg || t('knowledgeBase.chunkLoadFailed');
        console.error("[ChunkLoad] failed", {
          knowledgeId: id,
          page,
          error: err,
        });
      })
      .finally(() => {
        if (requestGeneration === chunkRequestGeneration) {
          details.chunkLoading = false;
        }
      });
  };
  return {
    cardList,
    moreIndex,
    getKnowled,
    details,
    openMore,
    onVisibleChange,
    getCardDetails,
    total,
    getfDetails,
  };
}
