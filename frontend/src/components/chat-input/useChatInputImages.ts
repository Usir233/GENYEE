import { ref, type ComputedRef, type Ref } from 'vue';
import { MessagePlugin } from 'tdesign-vue-next';
import type { AttachmentFile } from '../AttachmentUpload.vue';
import type { UploadedImagePreview } from './types';

export const CHAT_FILE_DROP_EVENT = 'genyee:chat-file-drop';

export interface AttachmentUploadHandle {
  addFiles: (files: File[]) => void;
  clear: () => void;
  triggerFileSelect: () => void;
}

export const isImageFile = (file: File): boolean => {
  if (file.type.startsWith('image/')) {
    return true;
  }
  const fileName = file.name.toLowerCase();
  return ['.jpg', '.jpeg', '.png', '.gif', '.webp', '.bmp'].some((ext) => fileName.endsWith(ext));
};

export function useChatInputImages(options: {
  isImageUploadEnabledByAgent: ComputedRef<boolean>;
  attachmentUploadRef: Ref<AttachmentUploadHandle | undefined>;
  t: (key: string, params?: Record<string, unknown>) => string;
}) {
  const { isImageUploadEnabledByAgent, attachmentUploadRef, t } = options;

  const uploadedImages = ref<UploadedImagePreview[]>([]);
  const imageInputRef = ref<HTMLInputElement>();
  const uploadedAttachments = ref<AttachmentFile[]>([]);

  const addImageFiles = (files: File[]): void => {
    if (!isImageUploadEnabledByAgent.value) return;
    const allowed = ['image/jpeg', 'image/png', 'image/gif', 'image/webp'];
    const maxSize = 10 * 1024 * 1024;
    for (const file of files) {
      if (uploadedImages.value.length >= 5) {
        MessagePlugin.warning(t('chat.imageTooMany'));
        break;
      }
      if (!allowed.includes(file.type)) {
        MessagePlugin.warning(t('chat.imageTypeSizeError'));
        continue;
      }
      if (file.size > maxSize) {
        MessagePlugin.warning(t('chat.imageTypeSizeError'));
        continue;
      }
      uploadedImages.value.push({ file, preview: URL.createObjectURL(file) });
    }
  };

  const removeImage = (index: number): void => {
    const removed = uploadedImages.value.splice(index, 1);
    if (removed.length > 0) URL.revokeObjectURL(removed[0].preview);
  };

  const revokeAllUploadedImages = (): void => {
    uploadedImages.value.forEach((img) => URL.revokeObjectURL(img.preview));
    uploadedImages.value = [];
  };

  const clearPendingUploads = (): void => {
    revokeAllUploadedImages();
    attachmentUploadRef.value?.clear();
    uploadedAttachments.value = [];
  };

  const triggerImageUpload = (): void => {
    imageInputRef.value?.click();
  };

  const handleImageSelect = (event: Event): void => {
    const input = event.target as HTMLInputElement;
    if (!input.files) return;
    addImageFiles(Array.from(input.files));
    input.value = '';
  };

  const handleDroppedFiles = (files: File[]): void => {
    if (!files.length) return;

    const imageFiles = files.filter(isImageFile);
    const attachmentFiles = files.filter((file) => !isImageFile(file));

    if (imageFiles.length > 0) {
      if (isImageUploadEnabledByAgent.value) {
        addImageFiles(imageFiles);
      } else {
        MessagePlugin.warning(t('input.imageUploadDisabledByAgent'));
      }
    }

    if (attachmentFiles.length > 0) {
      attachmentUploadRef.value?.addFiles(attachmentFiles);
    }
  };

  const handleChatFileDrop = (event: Event): void => {
    const customEvent = event as CustomEvent<{ files?: File[] }>;
    const files = customEvent.detail?.files;
    if (!files || files.length === 0) return;
    handleDroppedFiles(files);
  };

  const onPaste = (e: ClipboardEvent): void => {
    const items = e.clipboardData?.items;
    if (!items) return;
    const imageFiles: File[] = [];
    for (const item of items) {
      if (item.type.startsWith('image/')) {
        const file = item.getAsFile();
        if (file) imageFiles.push(file);
      }
    }
    if (imageFiles.length > 0 && isImageUploadEnabledByAgent.value) {
      e.preventDefault();
      addImageFiles(imageFiles);
    }
  };

  const onDrop = (e: DragEvent): void => {
    e.preventDefault();
    const files = e.dataTransfer?.files;
    if (!files || files.length === 0) return;
    handleDroppedFiles(Array.from(files));
  };

  const onDragOver = (e: DragEvent): void => {
    e.preventDefault();
  };

  return {
    uploadedImages,
    imageInputRef,
    uploadedAttachments,
    addImageFiles,
    removeImage,
    revokeAllUploadedImages,
    clearPendingUploads,
    triggerImageUpload,
    handleImageSelect,
    handleDroppedFiles,
    handleChatFileDrop,
    onPaste,
    onDrop,
    onDragOver,
  };
}
