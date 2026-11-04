import {test} from 'node:test'
import assert from 'node:assert/strict'
import {copyText} from '../src/clipboard.js'

function environment(t,clipboard,result=true){
 let copied='',removed=false,focused=false,restored=false
 const field={style:{},focus(){},select(){},setSelectionRange(){},remove(){removed=true}}
 const active={selectionStart:1,selectionEnd:3,selectionDirection:'forward',focus(){focused=true},setSelectionRange(start,end,direction){restored=start===1&&end===3&&direction==='forward'}}
 const previousNavigator=Object.getOwnPropertyDescriptor(globalThis,'navigator')
 const previousDocument=Object.getOwnPropertyDescriptor(globalThis,'document')
 t.after(()=>{
  if(previousNavigator)Object.defineProperty(globalThis,'navigator',previousNavigator);else delete globalThis.navigator
  if(previousDocument)Object.defineProperty(globalThis,'document',previousDocument);else delete globalThis.document
 })
 Object.defineProperty(globalThis,'navigator',{configurable:true,value:{clipboard}})
 Object.defineProperty(globalThis,'document',{configurable:true,value:{
  activeElement:active,getSelection:()=>null,createElement:()=>field,
  body:{appendChild(){}},execCommand(command){assert.equal(command,'copy');copied=field.value;return result}
 }})
 return ()=>({copied,removed,focused,restored})
}

test('modern clipboard writes the exact value without a DOM fallback',async t=>{
 let copied
 const state=environment(t,{async writeText(value){copied=value}})
 await copyText('sk-secret')
 assert.equal(copied,'sk-secret')
 assert.equal(state().removed,false)
})
test('HTTP without Clipboard API uses selection copy and restores focus',async t=>{
 const state=environment(t,undefined)
 await copyText('http://gateway.test/#contribute=example')
 assert.deepEqual(state(),{copied:'http://gateway.test/#contribute=example',removed:true,focused:true,restored:true})
})
test('denied modern clipboard falls back to selection copy',async t=>{
 const state=environment(t,{async writeText(){throw new Error('NotAllowedError')}})
 await copyText('sk-secret')
 assert.equal(state().copied,'sk-secret')
 assert.equal(state().removed,true)
})
test('failed fallback rejects and cleans up rather than reporting success',async t=>{
 const state=environment(t,undefined,false)
 await assert.rejects(copyText('secret'),/Clipboard copy failed/)
 assert.equal(state().removed,true)
 assert.equal(state().focused,true)
})
