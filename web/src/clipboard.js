// Prefer the Clipboard API; HTTP LAN deployments need the selection fallback.
export async function copyText(value) {
 const text=String(value)
 if(globalThis.navigator?.clipboard?.writeText){
  try{await globalThis.navigator.clipboard.writeText(text);return}catch{}
 }
 const active=document.activeElement
 const selection=document.getSelection()
 const ranges=[]
 if(selection)for(let i=0;i<selection.rangeCount;i++)ranges.push(selection.getRangeAt(i).cloneRange())
 const start=active?.selectionStart,end=active?.selectionEnd,direction=active?.selectionDirection
 const field=document.createElement('textarea')
 field.value=text
 field.readOnly=true
 field.style.cssText='position:fixed;top:0;left:-9999px;opacity:0;'
 try{
  document.body.appendChild(field)
  field.focus({preventScroll:true})
  field.select()
  field.setSelectionRange(0,text.length)
  if(!document.execCommand('copy'))throw new Error('Clipboard copy failed')
 }finally{
  field.remove()
  active?.focus?.({preventScroll:true})
  if(typeof start==='number'&&typeof end==='number')active.setSelectionRange(start,end,direction)
  if(selection){selection.removeAllRanges();for(const range of ranges)selection.addRange(range)}
 }
}
