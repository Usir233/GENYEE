<template>
  <t-input
    v-if="kind === 'text'"
    :model-value="typeof modelValue === 'string' ? modelValue : ''"
    :placeholder="placeholder"
    class="field-control--text"
    @update:model-value="emit('update:modelValue', $event)"
  />
  <t-input-number
    v-else-if="kind === 'tokens' || kind === 'number'"
    :model-value="typeof modelValue === 'number' ? modelValue : undefined"
    theme="normal"
    :min="0"
    :placeholder="placeholder"
    class="field-control--number"
    @update:model-value="emit('update:modelValue', $event)"
  />
  <t-switch
    v-else-if="kind === 'bool'"
    :model-value="Boolean(modelValue)"
    @update:model-value="emit('update:modelValue', Boolean($event))"
  />
  <t-checkbox-group
    v-else-if="kind === 'modalities'"
    :model-value="Array.isArray(modelValue) ? modelValue : []"
    :options="modalityOptions"
    @update:model-value="emit('update:modelValue', $event as string[])"
  />
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { EDITABLE_INPUTS, type FieldKind } from './modelCatalogState'

export type CatalogFieldControlValue = string | number | boolean | string[] | undefined

defineProps<{
  kind: FieldKind
  modelValue: CatalogFieldControlValue
  placeholder?: string
}>()
const emit = defineEmits<{
  'update:modelValue': [value: CatalogFieldControlValue]
}>()
const { t } = useI18n()
const modalityOptions = computed(() =>
  EDITABLE_INPUTS.map(value => ({ label: t(`modelCatalog.capability.${value}`), value })),
)
</script>

<style lang="less" scoped>
.field-control--text {
  width: 200px;
}

.field-control--number {
  width: 160px;
}
</style>
