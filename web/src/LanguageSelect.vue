<script setup>
import { locale, setLocale } from './i18n.js'
</script>
<template>
  <select class="language-select" aria-label="Language / 语言" :value="locale" @change="setLocale($event.target.value)">
    <option value="zh-CN">简体中文</option>
    <option value="en">English</option>
  </select>
</template>
