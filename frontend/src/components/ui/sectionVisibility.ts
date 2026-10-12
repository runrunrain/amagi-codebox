import type { InjectionKey, Ref } from 'vue';
export const EDITOR_SECTION_ACTIVE: InjectionKey<Readonly<Ref<boolean>>> = Symbol('editor-section-active');
