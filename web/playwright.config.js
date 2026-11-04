import { defineConfig } from '@playwright/test'
export default defineConfig({ testDir:'./tests', workers:1, timeout:30000, use:{ headless:true,locale:'zh-CN',timezoneId:'Asia/Shanghai',viewport:{width:1440,height:1000} }, reporter:'list' })
