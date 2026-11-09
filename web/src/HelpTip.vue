<script setup>
import { nextTick, onBeforeUnmount, ref } from 'vue'
defineProps({text:{type:String,required:true},label:{type:String,required:true}})
const button=ref(null),tip=ref(null),shown=ref(false),position=ref({})
async function show(){
 shown.value=true
 await nextTick()
 const anchor=button.value?.getBoundingClientRect(),box=tip.value?.getBoundingClientRect()
 if(!anchor||!box)return
 const gap=8,pad=12
 let left=Math.min(Math.max(anchor.left,pad),window.innerWidth-box.width-pad)
 let top=anchor.bottom+gap
 if(top+box.height>window.innerHeight-pad)top=Math.max(pad,anchor.top-box.height-gap)
 position.value={left:`${left}px`,top:`${top}px`}
}
function hide(){shown.value=false}
function dismiss(){hide()}
window.addEventListener('scroll',dismiss,true)
window.addEventListener('resize',dismiss)
onBeforeUnmount(()=>{window.removeEventListener('scroll',dismiss,true);window.removeEventListener('resize',dismiss)})
</script>
<template>
 <button ref="button" type="button" class="help-tip" :aria-label="label" :aria-expanded="shown" @mouseenter="show" @mouseleave="hide" @focus="show" @blur="hide" @click.prevent.stop>?</button>
 <Teleport to="body"><div v-if="shown" ref="tip" class="help-tooltip" role="tooltip" :style="position">{{ text }}</div></Teleport>
</template>
<style scoped>
.help-tip{display:inline-flex;align-items:center;justify-content:center;width:17px;height:17px;flex:0 0 17px;padding:0;border:1px solid #98a2b3;border-radius:50%;color:#667085;font-size:11px;line-height:1}
.help-tooltip{position:fixed;z-index:1000;width:max-content;max-width:min(380px,calc(100vw - 24px));padding:9px 11px;border-radius:8px;background:#344054;color:#fff;font-size:12px;font-weight:400;line-height:1.55;white-space:normal;box-shadow:0 6px 18px #10182826;pointer-events:none}
</style>
