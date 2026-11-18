// A label and its value are joined with the SDK's labelSeparator, so Chinese
// and Japanese get a full-width colon and the messages carry no colon of their own.
import {expect,test} from 'vitest'
import {readdirSync,readFileSync} from 'node:fs'
import {labelled} from '../src/i18n'
const LABELS=['connect.ipv6.opens','connect.check.checkedAt','connect.check.failed']
test('a label and value use the locale separator',()=>{
 expect(labelled('cht','connect.ipv6.opens','DSM HTTPS')).toBe('待檢查的連接埠：DSM HTTPS')
 expect(labelled('jpn','connect.check.checkedAt','x')).toBe('最終確認：x')
 expect(labelled('enu','connect.ipv6.opens','DSM HTTPS')).toBe('Ports to check: DSM HTTPS')
})
test('label messages leave the separator to the code',()=>{
 for(const file of readdirSync('../i18n/messages')){
  const messages=JSON.parse(readFileSync(`../i18n/messages/${file}`,'utf8')) as Record<string,string>
  for(const key of LABELS)expect(messages[key],`${file} ${key}`).not.toMatch(/[:：]\s*$/)
 }
})
