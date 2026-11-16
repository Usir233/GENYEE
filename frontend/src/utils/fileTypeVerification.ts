const DEFAULT_VALID_TYPES = new Set([
  "pdf",
  "txt",
  "md",
  "docx",
  "doc",
  "pptx",
  "ppt",
  "epub",
  "xmind",
  "html",
  "htm",
  "mhtml",
  "jpg",
  "jpeg",
  "png",
  "csv",
  "xlsx",
  "xls",
  "mp3",
  "wav",
  "m4a",
  "flac",
  "ogg",
]);

export function shouldRejectKnowledgeFileType(
  filename: string,
  validTypes?: Set<string> | string[],
) {
  const dotIdx = filename.lastIndexOf(".");
  if (dotIdx < 0 || dotIdx === filename.length - 1) return true;

  const provided = validTypes
    ? new Set(
        Array.from(validTypes)
          .map((t) => String(t).trim().replace(/^\./, "").toLowerCase())
          .filter(Boolean),
      )
    : undefined;
  const allowed = provided && provided.size > 0 ? provided : DEFAULT_VALID_TYPES;
  const type = filename.substring(dotIdx + 1).toLowerCase();

  return !allowed.has(type);
}
