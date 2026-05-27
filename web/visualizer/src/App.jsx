import { useState, useEffect, useMemo, useRef, useCallback } from 'react'
import { Canvas, useFrame } from '@react-three/fiber'
import { OrbitControls, Box, Cylinder, Line, Grid, Html } from '@react-three/drei'
import * as THREE from 'three'

const API = ''
const LINK_COLORS = {
  walk: '#60a5fa',
  jump: '#facc15',
  step_jump: '#f472b6',
  fall: '#f87171',
}

function linkColor(link) {
  return LINK_COLORS[link] || LINK_COLORS.walk
}

function StatusBadge({ connected, movement }) {
  return (
    <span
      className={`inline-flex items-center gap-2 px-3 py-1 rounded-full text-xs font-medium uppercase tracking-wide ${
        connected ? 'bg-emerald-500/15 text-emerald-400' : 'bg-red-500/15 text-red-400'
      }`}
    >
      <span className={`w-2 h-2 rounded-full ${connected ? 'bg-emerald-400 animate-pulse' : 'bg-red-400'}`} />
      {connected ? movement || 'live' : 'offline'}
    </span>
  )
}

function Panel({ title, children, className = '' }) {
  return (
    <div
      className={`p-5 bg-[#141312]/92 backdrop-blur-xl border border-white/[0.06] rounded-2xl shadow-xl text-[#e8e6e3] ${className}`}
    >
      {title && <h2 className="text-xs font-semibold uppercase tracking-widest text-[#8a8680] mb-3">{title}</h2>}
      {children}
    </div>
  )
}

const BotEntity = ({ position }) => {
  if (!position) return null
  return (
    <group position={[position.x, position.y + 0.9, position.z]}>
      <Cylinder args={[0.3, 0.3, 1.8, 16]}>
        <meshStandardMaterial color="#4ade80" emissive="#22c55e" emissiveIntensity={0.6} />
      </Cylinder>
      <Html distanceFactor={14} position={[0, 2.2, 0]} center>
        <div className="px-2 py-0.5 rounded bg-black/70 text-[10px] text-emerald-300 whitespace-nowrap font-mono">
          bot
        </div>
      </Html>
    </group>
  )
}

const Marker = ({ position, color, label, wireframe = false }) => {
  if (!position) return null
  const x = position.x ?? 0
  const y = position.y ?? 0
  const z = position.z ?? 0

  return (
    <group position={[x, y, z]}>
      <Box args={[0.75, 0.75, 0.75]}>
        <meshStandardMaterial color={color} emissive={color} emissiveIntensity={0.7} wireframe={wireframe} />
      </Box>
      {label && (
        <Html distanceFactor={14} position={[0, 1.1, 0]} center>
          <div className="px-2 py-0.5 rounded bg-black/70 text-[10px] whitespace-nowrap" style={{ color }}>
            {label}
          </div>
        </Html>
      )}
    </group>
  )
}

const PathRenderer = ({ path, currentIndex, variant = 'live' }) => {
  if (!path?.length) return null

  const points = path.map((node) => new THREE.Vector3(node.x + 0.5, node.y + 0.5, node.z + 0.5))
  const lineColor = variant === 'preview' ? '#22d3ee' : '#a855f7'
  const lineWidth = variant === 'preview' ? 3 : 4

  return (
    <group>
      <Line points={points} color={lineColor} lineWidth={lineWidth} />
      {path.map((node, i) => {
        const color = linkColor(node.link)
        const active = variant === 'live' && i === currentIndex
        return (
          <Box key={`${variant}-${i}`} args={[0.22, 0.22, 0.22]} position={[node.x + 0.5, node.y + 0.5, node.z + 0.5]}>
            <meshStandardMaterial
              color={color}
              emissive={color}
              emissiveIntensity={active ? 1.2 : variant === 'preview' ? 0.45 : 0.25}
              transparent
              opacity={variant === 'preview' ? 0.85 : 1}
            />
          </Box>
        )
      })}
    </group>
  )
}

const BlocksInstanced = ({ blocks }) => {
  const meshRef = useRef()
  const dummy = useMemo(() => new THREE.Object3D(), [])

  useEffect(() => {
    if (!meshRef.current || !blocks?.length) return
    blocks.forEach((block, i) => {
      dummy.position.set(block.x + 0.5, block.y + 0.5, block.z + 0.5)
      dummy.updateMatrix()
      meshRef.current.setMatrixAt(i, dummy.matrix)
    })
    meshRef.current.count = blocks.length
    meshRef.current.instanceMatrix.needsUpdate = true
  }, [blocks, dummy])

  if (!blocks?.length) return null

  return (
    <instancedMesh ref={meshRef} args={[null, null, blocks.length]} frustumCulled={false}>
      <boxGeometry args={[0.98, 0.98, 0.98]} />
      <meshStandardMaterial color="#374151" roughness={0.85} metalness={0.1} transparent opacity={0.72} />
    </instancedMesh>
  )
}

function ClickPlane({ enabled, yLevel, center, onPick }) {
  if (!enabled || center == null) return null
  const planeY = yLevel + 0.02

  return (
    <mesh
      rotation={[-Math.PI / 2, 0, 0]}
      position={[center.x, planeY, center.z]}
      onPointerDown={(e) => {
        e.stopPropagation()
        onPick({
          x: Math.floor(e.point.x),
          y: yLevel,
          z: Math.floor(e.point.z),
        })
      }}
    >
      <planeGeometry args={[128, 128]} />
      <meshBasicMaterial visible={false} />
    </mesh>
  )
}

function SmoothOrbit({ target, follow }) {
  const controlsRef = useRef()
  const smooth = useRef(new THREE.Vector3(0, 0, 0))
  const initialized = useRef(false)

  useFrame((_, delta) => {
    const ctrl = controlsRef.current
    if (!ctrl || !target) return
    if (!initialized.current) {
      smooth.current.copy(target)
      initialized.current = true
    }
    if (follow) {
      smooth.current.lerp(target, 1 - Math.exp(-10 * delta))
      ctrl.target.copy(smooth.current)
    } else {
      ctrl.target.copy(target)
    }
  })

  return <OrbitControls ref={controlsRef} makeDefault dampingFactor={0.08} minDistance={4} maxDistance={80} />
}

function Scene({
  state,
  blocks,
  debugTarget,
  previewPath,
  debugMode,
  pickY,
  onPick,
}) {
  const botPos = state.bot_pos
  const gridY = botPos ? Math.floor(botPos.y) : 0
  const cameraTarget = useMemo(() => {
    if (!botPos) return new THREE.Vector3(0, 0, 0)
    return new THREE.Vector3(botPos.x, botPos.y, botPos.z)
  }, [botPos?.x, botPos?.y, botPos?.z])

  const debugMarker = debugTarget
    ? { x: debugTarget.x + 0.5, y: debugTarget.y + 0.5, z: debugTarget.z + 0.5 }
    : null

  return (
    <>
      <color attach="background" args={['#0c0b0a']} />
      <fog attach="fog" args={['#0c0b0a', 35, 90]} />
      <ambientLight intensity={0.55} />
      <directionalLight position={[40, 80, 30]} intensity={1.2} />
      <pointLight position={[-15, 25, -15]} intensity={0.4} color="#4ade80" />

      <SmoothOrbit target={cameraTarget} follow={state.followCamera !== false} />

      <Grid
        position={[cameraTarget.x, gridY, cameraTarget.z]}
        infiniteGrid
        fadeDistance={55}
        cellColor="#2a2927"
        sectionColor="#3d4a3a"
        cellThickness={0.4}
        sectionThickness={0.9}
      />

      <BlocksInstanced blocks={blocks} />
      <PathRenderer path={state.path} currentIndex={state.path_index} variant="live" />
      <PathRenderer path={previewPath} variant="preview" />
      <BotEntity position={botPos} />
      <Marker position={state.target_pos} color="#f97316" label="target" wireframe />
      <Marker position={debugMarker} color="#22d3ee" label="debug click" />

      <ClickPlane
        enabled={debugMode}
        yLevel={pickY}
        center={botPos || { x: 0, y: 0, z: 0 }}
        onPick={onPick}
      />
    </>
  )
}

function useBotSocket(setState, setBlocks, setConnected) {
  useEffect(() => {
    let ws
    let retryTimer
    let closed = false

    const connect = () => {
      const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
      ws = new WebSocket(`${proto}://${window.location.host}/ws`)

      ws.onopen = () => setConnected(true)
      ws.onclose = () => {
        setConnected(false)
        if (!closed) retryTimer = setTimeout(connect, 1500)
      }
      ws.onerror = () => ws.close()

      ws.onmessage = (event) => {
        const data = JSON.parse(event.data)
        setState((prev) => ({ ...prev, ...data, lastUpdate: Date.now() }))
        if (data.blocks) setBlocks(data.blocks)
      }
    }

    connect()
    return () => {
      closed = true
      clearTimeout(retryTimer)
      ws?.close()
    }
  }, [setState, setBlocks, setConnected])
}

async function previewPathAt(coord) {
  const res = await fetch(`${API}/api/path/preview`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(coord),
  })
  if (!res.ok) throw new Error(await res.text())
  const data = await res.json()
  return data.preview
}

async function walkTo(coord) {
  const res = await fetch(`${API}/api/walk`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(coord),
  })
  if (!res.ok) throw new Error(await res.text())
  return res.json()
}

function App() {
  const [state, setState] = useState({ followCamera: true })
  const [blocks, setBlocks] = useState([])
  const [connected, setConnected] = useState(false)
  const [debugMode, setDebugMode] = useState(true)
  const [debugTarget, setDebugTarget] = useState(null)
  const [previewPath, setPreviewPath] = useState(null)
  const [previewMeta, setPreviewMeta] = useState(null)
  const [pickY, setPickY] = useState(0)
  const [loadingPreview, setLoadingPreview] = useState(false)
  const [lastSync, setLastSync] = useState(null)

  useBotSocket(setState, setBlocks, setConnected)

  useEffect(() => {
    if (state.bot_pos) {
      setPickY((y) => {
        if (y === 0 && state.bot_pos) return Math.floor(state.bot_pos.y)
        return y
      })
    }
  }, [state.bot_pos?.y])

  useEffect(() => {
    if (state.lastUpdate) setLastSync(new Date(state.lastUpdate))
  }, [state.lastUpdate])

  const handlePick = useCallback(
    async (coord) => {
      setDebugTarget(coord)
      setLoadingPreview(true)
      try {
        const preview = await previewPathAt(coord)
        setPreviewPath(preview.path)
        setPreviewMeta(preview)
      } catch (e) {
        console.error(e)
        setPreviewPath(null)
        setPreviewMeta({ found: false, error: String(e) })
      } finally {
        setLoadingPreview(false)
      }
    },
    [],
  )

  const handleWalk = async () => {
    if (!debugTarget) return
    try {
      await walkTo(debugTarget)
    } catch (e) {
      console.error(e)
    }
  }

  const syncAge = lastSync ? `${Math.max(0, Math.round((Date.now() - lastSync.getTime()) / 1000))}s ago` : '—'

  return (
    <div className="w-screen h-screen bg-[#0c0b0a] overflow-hidden relative select-none">
      {/* Header */}
      <div className="absolute top-0 left-0 right-0 z-20 flex items-center justify-between px-5 py-4 pointer-events-none">
        <div className="pointer-events-auto flex items-center gap-4">
          <h1 className="text-lg font-semibold text-[#f5f3ef] tracking-tight" style={{ fontFamily: 'Georgia, serif' }}>
            Pathfinder Debug
          </h1>
          <StatusBadge connected={connected} movement={state.movement_state} />
        </div>
        <div className="pointer-events-auto text-xs text-[#8a8680] font-mono">
          sync {syncAge} · {blocks.length} blocks · tick {state.server_tick ?? '—'}
        </div>
      </div>

      {/* Left sidebar */}
      <div className="absolute top-20 left-5 z-10 flex flex-col gap-3 w-72 max-h-[calc(100vh-6rem)] overflow-y-auto pointer-events-auto">
        <Panel title="Bot">
          <div className="space-y-2 text-sm font-mono">
            <Row label="Position" value={fmtVec(state.bot_pos)} />
            <Row label="Target" value={fmtVec(state.target_pos)} />
            <Row label="Live path" value={`${state.path?.length ?? 0} nodes · idx ${state.path_index ?? 0}`} />
          </div>
        </Panel>

        <Panel title="Debug pathfinder">
          <label className="flex items-center gap-2 text-sm mb-3 cursor-pointer">
            <input
              type="checkbox"
              checked={debugMode}
              onChange={(e) => setDebugMode(e.target.checked)}
              className="rounded accent-cyan-500"
            />
            <span>Click-to-preview mode</span>
          </label>

          <div className="mb-3">
            <label className="text-xs text-[#8a8680] block mb-1">Click Y level (block)</label>
            <input
              type="number"
              value={pickY}
              onChange={(e) => setPickY(parseInt(e.target.value, 10) || 0)}
              className="w-full px-3 py-2 rounded-lg bg-black/40 border border-white/10 text-sm font-mono"
            />
          </div>

          <p className="text-xs text-[#8a8680] mb-3 leading-relaxed">
            Klik pada grid untuk simulasi path dari posisi bot sekarang. Garis <span className="text-cyan-400">cyan</span>{' '}
            = preview, <span className="text-purple-400">ungu</span> = path aktif bot.
          </p>

          {debugTarget && (
            <div className="mb-3 p-3 rounded-lg bg-cyan-500/10 border border-cyan-500/20 text-sm">
              <div className="text-cyan-300 font-mono mb-1">
                {debugTarget.x}, {debugTarget.y}, {debugTarget.z}
              </div>
              {loadingPreview ? (
                <span className="text-[#8a8680]">Menghitung path…</span>
              ) : previewMeta ? (
                <span className={previewMeta.found ? 'text-emerald-400' : 'text-red-400'}>
                  {previewMeta.found
                    ? `${previewMeta.node_count} nodes${previewMeta.used_scaffold ? ' · scaffold' : ''}${previewMeta.used_fallback ? ' · fallback' : ''}`
                    : 'Path tidak ditemukan'}
                </span>
              ) : null}
            </div>
          )}

          <div className="flex gap-2">
            <button
              type="button"
              disabled={!debugTarget || loadingPreview}
              onClick={() => debugTarget && handlePick(debugTarget)}
              className="flex-1 py-2 rounded-lg bg-white/5 hover:bg-white/10 text-sm disabled:opacity-40"
            >
              Recalc
            </button>
            <button
              type="button"
              disabled={!debugTarget || !previewMeta?.found}
              onClick={handleWalk}
              className="flex-1 py-2 rounded-lg bg-emerald-600/80 hover:bg-emerald-500 text-sm font-medium disabled:opacity-40"
            >
              Walk here
            </button>
          </div>
        </Panel>

        <Panel title="Legend">
          <ul className="space-y-1.5 text-xs">
            {Object.entries(LINK_COLORS).map(([k, c]) => (
              <li key={k} className="flex items-center gap-2">
                <span className="w-3 h-3 rounded-sm" style={{ background: c }} />
                <span className="text-[#a19e99]">{k}</span>
              </li>
            ))}
            <li className="flex items-center gap-2 pt-1 border-t border-white/5">
              <span className="w-3 h-3 rounded-sm bg-cyan-400" />
              <span className="text-[#a19e99]">preview path</span>
            </li>
          </ul>
        </Panel>
      </div>

      {/* Right controls */}
      <div className="absolute top-20 right-5 z-10 pointer-events-auto">
        <Panel>
          <label className="flex items-center gap-2 text-sm cursor-pointer">
            <input
              type="checkbox"
              checked={state.followCamera !== false}
              onChange={(e) => setState((s) => ({ ...s, followCamera: e.target.checked }))}
              className="rounded accent-emerald-500"
            />
            Follow bot (smooth)
          </label>
        </Panel>
      </div>

      {/* Hint */}
      {debugMode && (
        <div className="absolute bottom-6 left-1/2 -translate-x-1/2 z-10 px-4 py-2 rounded-full bg-black/60 border border-white/10 text-xs text-[#a19e99] pointer-events-none">
          Klik di dunia untuk preview path · Walk here untuk kirim bot
        </div>
      )}

      <Canvas camera={{ position: [18, 16, 18], fov: 52 }} className="touch-none">
        <Scene
          state={state}
          blocks={blocks}
          debugTarget={debugTarget}
          previewPath={previewPath}
          debugMode={debugMode}
          pickY={pickY}
          onPick={handlePick}
        />
      </Canvas>
    </div>
  )
}

function Row({ label, value }) {
  return (
    <div className="flex justify-between gap-2">
      <span className="text-[#8a8680]">{label}</span>
      <span className="text-[#e8e6e3] text-right">{value}</span>
    </div>
  )
}

function fmtVec(v) {
  if (!v) return '—'
  return `${v.x.toFixed(1)}, ${v.y.toFixed(1)}, ${v.z.toFixed(1)}`
}

export default App
