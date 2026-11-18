// Curated sample texts for the chunking debug drawer. Each preset is sized
// to ≈2000–4000 characters and shaped to exercise a distinct tier of the
// chunker, so users can quickly see how their config behaves on realistic
// content without preparing their own sample.

import i18n from '@/i18n'

export interface ChunkingSample {
  id: string
  // i18n key under knowledgeEditor.chunking.debug.samples.<id>
  labelKey: string
  readonly text: string
}

export const CHUNKING_SAMPLES: ChunkingSample[] = [
  {
    id: 'markdown',
    labelKey: 'samples.markdown',
    get text() {
      return i18n.global.t('knowledgeEditor.chunking.debug.sampleTexts.markdown')
    },
  },
  {
    id: 'faq',
    labelKey: 'samples.faq',
    get text() {
      return i18n.global.t('knowledgeEditor.chunking.debug.sampleTexts.faq')
    },
  },
  {
    id: 'chapter',
    labelKey: 'samples.chapter',
    get text() {
      return i18n.global.t('knowledgeEditor.chunking.debug.sampleTexts.chapter')
    },
  },
  {
    id: 'plain',
    labelKey: 'samples.plain',
    get text() {
      return i18n.global.t('knowledgeEditor.chunking.debug.sampleTexts.plain')
    },
  },
]

export const DEFAULT_SAMPLE_ID = 'markdown'
