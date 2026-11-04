<script setup>
defineProps({name:{type:String,default:'grid'}})
const paths={
 copy:['M9 5H6v16h13V5h-3 M10 3h5v4h-5z'],
 grid:['M3 3h6v6H3z M15 3h6v6h-6z M3 15h6v6H3z M15 15h6v6h-6z'],
 key:['M14 3a7 7 0 1 0 7 7 7 7 0 0 0-7-7Z','M9 15l-7 7H0v-4l7-7','M15 7h.01'],
 logs:['M5 3h14v18H5z','M8 8h8 M8 12h8 M8 16h5'],
 users:['M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2','M16 3a4 4 0 0 1 0 8 M22 21v-2a4 4 0 0 0-3-3.9','M13 7a4 4 0 1 1-8 0 4 4 0 0 1 8 0Z'],
 accounts:['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z','M3 12h18 M12 3a18 18 0 0 1 0 18 18 18 0 0 1 0-18'],
 models:['M3 6c0-4 18-4 18 0s-18 4-18 0Z','M3 6v6c0 4 18 4 18 0V6 M3 12v6c0 4 18 4 18 0v-6'],
 clock:['M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z','M12 7v5l3 2'],
 audit:['M8 5H5v16h14V5h-3 M9 3h6v4H9z','M8 12h8 M8 16h6'],
 settings:['M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8Z','M12 2v3 M12 19v3 M2 12h3 M19 12h3 M5 5l2 2 M17 17l2 2 M5 19l2-2 M17 7l2-2'],
 chart:['M3 20h18 M5 16V9h3v7 M11 16V5h3v11 M17 16V2h3v14'],
 box:['M12 3l9 5v9l-9 5-9-5V8l9-5Z','M3 8l9 5 9-5 M12 13v9 M7 5l10 6'],
 bolt:['M13 2L3 14h8l-1 8 11-13h-8l1-7Z'],
 refresh:['M20 7a8 8 0 1 0 1 8','M20 2v5h-5'],
 chevron:['M9 5l7 7-7 7'],
 logout:['M9 3H4v18h5 M9 12h12 M16 7l5 5-5 5'],
 plus:['M12 5v14 M5 12h14'],
 menu:['M3 6h18 M3 12h18 M3 18h18'],
 moon:['M20 15a9 9 0 0 1-11-12A9 9 0 1 0 20 15Z']
}
</script>
<template><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path v-for="(d,i) in (paths[name]||paths.grid)" :key="i" :d="d"/></svg></template>
