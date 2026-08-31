<template>
  <div class="markdown-body" v-html="html"></div>
</template>

<script setup>
import DOMPurify from 'dompurify';
import { marked } from 'marked';
import { computed, defineProps } from 'vue';

const props = defineProps({
  source: {
    type: String,
    required: true,
  },
});

// Release notes are fetched from a remote API and are therefore untrusted.
// Parse Markdown first, then sanitize the resulting DOM before v-html renders it.
const html = computed(() => DOMPurify.sanitize(marked.parse(props.source), { USE_PROFILES: { html: true } }));
</script>

<style scoped>
@import "~github-markdown-css/github-markdown.css";
</style>
