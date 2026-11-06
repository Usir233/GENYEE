type UploadFile = Pick<File, 'size' | 'name' | 'type'>

export function jsonByteLength(value: unknown): number {
  return new TextEncoder().encode(JSON.stringify(value)).byteLength
}

/** Estimate the JSON body without allocating copies of the Base64 file data. */
export function inlineUploadsFitRequest(
  imageFiles: UploadFile[],
  attachmentFiles: UploadFile[],
  limitBytes: number,
  query = '',
): boolean {
  const files = [...imageFiles, ...attachmentFiles]
  const base64Bytes = files.reduce((sum, file) => sum + 4 * Math.ceil(file.size / 3), 0)
  const prefix = (file: UploadFile) => `data:${file.type || 'application/octet-stream'};base64,`
  const metadataBytes = jsonByteLength({
    query,
    images: imageFiles.map(file => ({ data: prefix(file) })),
    attachment_uploads: attachmentFiles.map(file => ({
      data: prefix(file), file_name: file.name, file_size: file.size,
    })),
  })
  // Agent/KB IDs, channel flags and attribution are added by the stream client.
  // It also checks the exact serialized body immediately before sending.
  return base64Bytes + metadataBytes + 16 * 1024 <= limitBytes
}
