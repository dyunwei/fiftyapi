<script setup>
import { t, locale } from './i18n.js'
const page=defineModel('page',{type:Number,default:1})
const size=defineModel('size',{type:Number,default:10})
defineProps({total:{type:Number,required:true},pages:{type:Number,required:true},label:{type:String,required:true}})
</script>
<template>
 <div class="model-pagination"><span>{{ t('共 {0} 项 · 第 {1} / {2} 页',[total,page,pages]) }}</span><div class="actions"><label class="page-size">{{ t('每页') }}<select :aria-label="t('{0}每页条数',[label])" v-model.number="size" @change="page=1"><option :value="10">10</option><option :value="20">20</option><option :value="50">50</option><option :value="100">100</option></select></label><button type="button" class="secondary" :disabled="page<=1" @click="page=1">{{ t('首页') }}</button><button type="button" class="secondary" :disabled="page<=1" @click="page--">{{ t('上一页') }}</button><button type="button" class="secondary" :disabled="page>=pages" @click="page++">{{ t('下一页') }}</button><button type="button" class="secondary" :disabled="page>=pages" @click="page=pages">{{ t('末页') }}</button></div></div>
</template>
