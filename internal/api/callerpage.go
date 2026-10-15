package api

// The page a 911 caller opens from the link the call-taker sends: share the camera live, or send
// photos and clips. Plain HTML, large controls, no outside resources, works on any phone browser.

const callerPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>911: share video</title>
<style>
 :root { color-scheme: dark; }
 body { margin: 0; font: 18px/1.45 system-ui, -apple-system, Segoe UI, Roboto, sans-serif; background: #0b1220; color: #e2e8f0; }
 main { max-width: 34rem; margin: 0 auto; padding: 1.25rem; }
 h1 { font-size: 1.5rem; margin: .25rem 0 1rem; color: #fecaca; }
 p { margin: .5rem 0; }
 button, label.file { display: block; width: 100%; box-sizing: border-box; margin: .75rem 0; padding: 1rem; border: 0; border-radius: .75rem;
   font: inherit; font-weight: 600; text-align: center; cursor: pointer; }
 #share { background: #dc2626; color: #fff; }
 #stop { background: #334155; color: #fff; display: none; }
 label.file { background: #0f766e; color: #fff; }
 input[type=file] { position: absolute; left: -9999px; }
 video { width: 100%; border-radius: .75rem; background: #000; display: none; margin-top: .5rem; }
 #status { min-height: 1.5rem; color: #fde68a; font-weight: 600; }
 .small { color: #94a3b8; font-size: .9rem; }
</style>
</head>
<body>
<main>
 <h1>Show 911 what is happening</h1>
 <p>Stay on the phone with the call-taker. What you share here goes only to 911.</p>
 <p id="status" role="status" aria-live="polite"></p>
 <button id="share" type="button">Share live video</button>
 <button id="stop" type="button">Stop sharing</button>
 <video id="preview" autoplay playsinline muted aria-label="What your camera is sending"></video>
 <label class="file" for="files">Send photos or videos</label>
 <input id="files" type="file" accept="image/*,video/*" multiple>
 <p class="small">Your phone will ask to use the camera. Do not put yourself in danger to take pictures.</p>
</main>
<script src="/v-app.js"></script>
</body>
</html>
`

const callerScript = `(() => {
  const token = location.pathname.split('/').filter(Boolean).pop()
  const $ = (id) => document.getElementById(id)
  const say = (t) => { $('status').textContent = t }
  let pc = null, resource = null, stream = null
  async function stop() {
    if (resource) { try { await fetch(resource, { method: 'DELETE' }) } catch (e) {} }
    if (pc) pc.close()
    if (stream) stream.getTracks().forEach((t) => t.stop())
    pc = resource = stream = null
    $('preview').style.display = 'none'; $('stop').style.display = 'none'; $('share').style.display = 'block'
  }
  $('share').addEventListener('click', async () => {
    try {
      say('Starting the camera…')
      stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: 'environment', width: { ideal: 1280 }, height: { ideal: 720 } }, audio: false })
      $('preview').srcObject = stream; $('preview').style.display = 'block'
      pc = new RTCPeerConnection()
      stream.getVideoTracks().forEach((t) => pc.addTransceiver(t, { direction: 'sendonly', streams: [stream] }))
      await pc.setLocalDescription(await pc.createOffer())
      const r = await fetch('/whip/' + encodeURIComponent(token), { method: 'POST', headers: { 'Content-Type': 'application/sdp' }, body: pc.localDescription.sdp })
      const text = await r.text()
      if (!r.ok) throw new Error(text)
      resource = r.headers.get('Location')
      await pc.setRemoteDescription({ type: 'answer', sdp: text })
      pc.addEventListener('connectionstatechange', () => {
        if (pc && pc.connectionState === 'connected') say('911 can see your video.')
        if (pc && (pc.connectionState === 'failed' || pc.connectionState === 'disconnected')) say('The video stopped. Try again, or send photos.')
      })
      $('share').style.display = 'none'; $('stop').style.display = 'block'
      say('Connecting to 911…')
    } catch (e) {
      say('Video could not start: ' + (e && e.message ? e.message : e) + '. You can still send photos.')
      await stop()
    }
  })
  $('stop').addEventListener('click', async () => { await stop(); say('You stopped sharing.') })
  $('files').addEventListener('change', async (ev) => {
    const files = Array.from(ev.target.files || [])
    let sent = 0
    for (const f of files) {
      say('Sending ' + f.name + '…')
      try {
        const r = await fetch('/v/' + encodeURIComponent(token) + '/upload?name=' + encodeURIComponent(f.name), { method: 'POST', headers: { 'Content-Type': f.type || 'application/octet-stream' }, body: f })
        if (!r.ok) throw new Error(await r.text())
        sent++
      } catch (e) { say(f.name + ' was not sent: ' + (e && e.message ? e.message : e)); return }
    }
    if (sent) say(sent === 1 ? 'Sent to 911.' : sent + ' files sent to 911.')
    ev.target.value = ''
  })
  window.addEventListener('pagehide', () => { if (resource) navigator.sendBeacon && fetch(resource, { method: 'DELETE', keepalive: true }) })
})()
`
