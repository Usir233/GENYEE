import { ref } from "vue";
import { defineStore } from "pinia";

export const knowledgeStore = defineStore("knowledge", {
  state: () => ({
    cardList: ref<Record<string, unknown>[]>([]),
    total: ref<number>(0),
  }),
  actions: {},
});
