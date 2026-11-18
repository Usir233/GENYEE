import { MessagePlugin } from 'tdesign-vue-next';
import {
  deleteTemporaryAttachment,
  uploadTemporaryAttachment,
} from '@/api/chat/temporary-attachments';
import type {
  AttachmentUploadItem,
  HostScopeSnapshot,
  MentionedItemInput,
  MessageImageDisplay,
} from '../types/chatView';

function fileToBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });
}

export interface PrepareOutgoingAttachmentsOptions {
  sessionId: string;
  embeddedMode: boolean;
  selectedAgentId?: string;
  selectedAgentSourceTenantId?: string;
  imageFiles: File[];
  attachmentFiles: AttachmentUploadItem[];
  t: (key: string) => string;
}

export interface PreparedOutgoingAttachments {
  imageAttachments: Array<{ data: string }>;
  userImages: MessageImageDisplay[];
  attachmentIds: string[];
  attachmentUploads: Array<{ data: string; file_name: string; file_size: number }>;
}

export async function prepareOutgoingAttachments(
  options: PrepareOutgoingAttachmentsOptions,
): Promise<PreparedOutgoingAttachments | null> {
  const {
    sessionId,
    embeddedMode,
    selectedAgentId,
    selectedAgentSourceTenantId,
    imageFiles,
    attachmentFiles,
    t,
  } = options;

  const imageAttachments: Array<{ data: string }> = [];
  const userImages: MessageImageDisplay[] = [];
  const imageAttachmentIds: string[] = [];

  if (imageFiles && imageFiles.length > 0) {
    for (const file of imageFiles) {
      let dataURI: string;
      try {
        dataURI = await fileToBase64(file);
      } catch (e: unknown) {
        console.error('[Image] Failed to read images:', e);
        return null;
      }
      userImages.push({ url: dataURI });
      if (embeddedMode) {
        imageAttachments.push({ data: dataURI });
        continue;
      }
      try {
        const upload = await uploadTemporaryAttachment(
          sessionId,
          file,
          selectedAgentId,
          selectedAgentSourceTenantId,
          'auto',
        );
        imageAttachmentIds.push(upload.data.id);
      } catch (e: unknown) {
        console.error('[Image] Temporary image upload failed, falling back to inline:', e);
        imageAttachments.push({ data: dataURI });
      }
    }
  }

  const localAttachments = (attachmentFiles || []).filter((attachment) => !attachment.documentId);
  if (!embeddedMode && localAttachments.length > 0) {
    try {
      await Promise.all(
        localAttachments.map(async (attachment) => {
          attachment.status = 'uploading';
          const upload = await uploadTemporaryAttachment(
            sessionId,
            attachment.file,
            selectedAgentId,
            selectedAgentSourceTenantId,
            'auto',
          );
          attachment.documentId = upload.data.id;
          attachment.status = upload.data.status;
        }),
      );
    } catch (uploadErr: unknown) {
      console.error('[Attachment] Temporary document upload failed:', uploadErr);
      await Promise.all(
        localAttachments
          .filter((attachment) => Boolean(attachment.documentId))
          .map((attachment) =>
            deleteTemporaryAttachment(sessionId, attachment.documentId as string).catch(
              () => undefined,
            ),
          ),
      );
      const errMsg =
        uploadErr instanceof Error && uploadErr.message
          ? uploadErr.message
          : t('chat.attachmentParseFailed');
      MessagePlugin.error(errMsg);
      return null;
    }
  }

  const attachmentIds = (attachmentFiles || [])
    .filter((attachment) => Boolean(attachment.documentId) && attachment.status !== 'failed')
    .map((attachment) => attachment.documentId as string);
  attachmentIds.push(...imageAttachmentIds);

  const legacyAttachmentFiles = embeddedMode
    ? (attachmentFiles || []).filter((attachment) => !attachment.documentId)
    : [];
  const attachmentUploads: Array<{ data: string; file_name: string; file_size: number }> = [];
  if (legacyAttachmentFiles.length > 0) {
    try {
      for (const attachment of legacyAttachmentFiles) {
        const dataUri = await fileToBase64(attachment.file);
        const base64Data = dataUri.split(',')[1] || '';
        attachmentUploads.push({
          data: base64Data,
          file_name: attachment.name,
          file_size: attachment.size,
        });
      }
    } catch (e: unknown) {
      console.error('[Attachment] Failed to read attachments:', e);
      return null;
    }
  }

  return {
    imageAttachments,
    userImages,
    attachmentIds,
    attachmentUploads,
  };
}

export function resolveOutgoingResourceScope(options: {
  embeddedMode: boolean;
  hostScope: HostScopeSnapshot | null;
  selectedKnowledgeBases?: string[];
  selectedFiles?: string[];
  pendingSuggestionKbIds: string[];
  mentionedItems: MentionedItemInput[];
}) {
  const {
    embeddedMode,
    hostScope,
    selectedKnowledgeBases,
    selectedFiles,
    pendingSuggestionKbIds,
    mentionedItems,
  } = options;

  const sidebarKbIds = embeddedMode
    ? hostScope?.knowledgeBaseIds || []
    : selectedKnowledgeBases || [];
  const sidebarFileIds = embeddedMode ? [] : selectedFiles || [];
  const kbIdSet = new Set<string>(sidebarKbIds);
  const fileIdSet = new Set<string>(sidebarFileIds);

  for (const kbId of pendingSuggestionKbIds) {
    if (kbId) kbIdSet.add(kbId);
  }
  for (const item of mentionedItems || []) {
    if (!item?.id) continue;
    if (item.type === 'kb' && !kbIdSet.has(item.id)) {
      kbIdSet.add(item.id);
    } else if (item.type === 'file' && !fileIdSet.has(item.id)) {
      fileIdSet.add(item.id);
    }
  }

  const tagIds = [
    ...new Set((mentionedItems || []).filter((item) => item.type === 'tag' && item.id).map((item) => item.id)),
  ];
  const mcpServiceIds = [
    ...new Set((mentionedItems || []).filter((item) => item.type === 'mcp' && item.id).map((item) => item.id)),
  ];
  const skillNames = [
    ...new Set(
      (mentionedItems || [])
        .filter((item) => item.type === 'skill' && item.id)
        .map((item) => item.skill_name || item.id),
    ),
  ];

  return {
    kbIds: [...kbIdSet],
    knowledgeIds: [...fileIdSet],
    tagIds,
    mcpServiceIds,
    skillNames,
  };
}
