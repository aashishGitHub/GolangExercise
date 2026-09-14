// Stamps a geotag/timestamp label onto the photo before it ever leaves the
// device — the field-assessment record needs to carry proof of where/when it
// was taken even if EXIF metadata is stripped later.
export async function watermarkImage(file: File | Blob, label: string): Promise<Blob> {
  const bitmap = await createImageBitmap(file)
  const canvas = document.createElement('canvas')
  canvas.width = bitmap.width
  canvas.height = bitmap.height

  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('canvas 2d context unavailable')
  ctx.drawImage(bitmap, 0, 0)

  const fontSize = Math.max(16, Math.round(canvas.width / 30))
  ctx.font = `${fontSize}px sans-serif`
  ctx.strokeStyle = 'rgba(0,0,0,0.85)'
  ctx.fillStyle = 'rgba(255,255,255,0.95)'
  ctx.lineWidth = 3
  const x = 12
  const y = canvas.height - 12
  ctx.strokeText(label, x, y)
  ctx.fillText(label, x, y)

  return await new Promise<Blob>((resolve, reject) => {
    canvas.toBlob(
      (blob) => (blob ? resolve(blob) : reject(new Error('canvas.toBlob failed'))),
      'image/jpeg',
      0.9,
    )
  })
}
