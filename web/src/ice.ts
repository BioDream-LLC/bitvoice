/** The TURN servers BitVoice offers browser phones, fetched with fresh credentials and kept an hour. */
let cached: { at: number; servers: RTCIceServer[] } | null = null

export async function iceServers(): Promise<RTCIceServer[]> {
  if (cached && Date.now() - cached.at < 3600e3) return cached.servers
  try {
    const r = await fetch('./api/ice', { credentials: 'same-origin' })
    const j = (await r.json()) as { iceServers?: RTCIceServer[] }
    cached = { at: Date.now(), servers: j.iceServers ?? [] }
  } catch {
    cached = { at: Date.now() - 3000e3, servers: [] } // try again in ten minutes
  }
  return cached.servers
}

/** A peer connection with BitVoice's TURN servers. Set localStorage bitvoice.iceRelay=1 to use only relays (tests). */
export async function newPeer(): Promise<RTCPeerConnection> {
  const servers = await iceServers()
  const relay = typeof localStorage !== 'undefined' && localStorage.getItem('bitvoice.iceRelay') === '1'
  return new RTCPeerConnection({ iceServers: servers, iceTransportPolicy: relay && servers.length ? 'relay' : 'all' })
}
