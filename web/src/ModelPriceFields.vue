<script setup>
import {ref} from 'vue'
import {t} from './i18n.js'
const form=defineModel({required:true})
const props=defineProps({api:Function})
const matches=ref([]),error=ref('')
async function search(){error.value='';try{matches.value=await props.api('/admin/prices/catalog?search='+encodeURIComponent(form.value.pricing_key||form.value.upstream_id||''))}catch(e){error.value=e.message}}
</script>
<template>
<label>{{t('定价方式')}}<select :aria-label="t('定价方式')" v-model="form.pricing_mode"><option value="keep">{{t('保持当前价格')}}</option><option value="catalog">{{t('跟随价格目录')}}</option><option value="manual">{{t('手工标准价格')}}</option></select></label>
<p v-if="form.price?.source==='litellm'" class="subtle">{{t('自动价格')}} · {{form.price.revision?.slice(0,12)}}</p>
<template v-if="form.pricing_mode==='catalog'">
<label>{{t('价格目录键')}}<input :aria-label="t('价格目录键')" v-model="form.pricing_key" maxlength="512" list="price-catalog-matches" required></label>
<button type="button" class="secondary" @click="search">{{t('搜索价格目录')}}</button>
<datalist id="price-catalog-matches"><option v-for="m in matches" :key="m.key" :value="m.key"/></datalist>
<p v-if="error" class="alert" role="alert">{{t(error)}}</p>
<p class="subtle">{{t('按供应商和模型精确匹配。选择后自动同步各档位价格，覆盖当前手工价格。')}}</p>
</template>
<template v-if="form.pricing_mode==='manual'">
<div class="two-columns"><label v-for="[key,label] in [['input',t('输入')],['output',t('输出')],['cached',t('缓存读取')],['cache_write',t('缓存写入')]]" :key="key">{{label}}{{t('（USD / 百万 Token）')}}<input v-model="form[key]" type="number" min="0" step="0.000001" required></label></div>
<p class="subtle">{{t('手工价格仅覆盖标准档位，并停止该模型的自动价格更新。')}}</p>
</template>
<table v-if="form.price?.rates?.length" class="model-price-table"><thead><tr><th>{{t('服务档位')}}</th><th>{{t('输入')}}</th><th>{{t('输出')}}</th></tr></thead><tbody><tr v-for="(r,i) in form.price.rates" :key="i"><td>{{r.tier}}{{r.above?' > '+r.above:''}}</td><td>{{r.input/1e6}}</td><td>{{r.output/1e6}}</td></tr></tbody></table>
</template>
