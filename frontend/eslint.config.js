import js from '@eslint/js'
import ts from 'typescript-eslint'
import vue from 'eslint-plugin-vue'
export default [
 {files:['**/*.mjs'],languageOptions:{globals:{process:'readonly',Buffer:'readonly',console:'readonly'}}},
 {ignores:['dist/**']},
 js.configs.recommended,
 ...ts.configs.recommended,
 ...vue.configs['flat/essential'],
 {files:['**/*.vue'],languageOptions:{parserOptions:{parser:ts.parser}}},
 {files:['**/*.{ts,vue}'],languageOptions:{globals:{URL:'readonly',navigator:'readonly',window:'readonly',document:'readonly',crypto:'readonly',HTMLElement:'readonly',HTMLScriptElement:'readonly',Record:'readonly',console:'readonly',requestAnimationFrame:'readonly',setTimeout:'readonly'}},rules:{'@typescript-eslint/no-explicit-any':'off'}}
]
