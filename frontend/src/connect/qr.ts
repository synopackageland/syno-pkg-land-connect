// A QR code drawn locally as one SVG path of dark modules; no service sees the text.
import {encode} from 'uqr'

export function qrPath(text:string):{size:number;d:string} {
 const {size,data}=encode(text)
 const d=data.flatMap((row,y)=>row.map((dark,x)=>dark?`M${x} ${y}h1v1h-1z`:'')).join('')
 return {size,d}
}
