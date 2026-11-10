import * as THREE from "three";

let cached: THREE.ExtrudeGeometry | null = null;

/** Shared gear geometry (extruded outline with teeth); built once for all module blocks. */
export function gearGeometry(): THREE.ExtrudeGeometry {
  if (cached) return cached;
  const teeth = 10;
  const rOut = 0.42;
  const rIn = 0.33;
  const shape = new THREE.Shape();
  for (let i = 0; i < teeth * 2; i++) {
    const r = i % 2 === 0 ? rOut : rIn;
    const a0 = (i / (teeth * 2)) * Math.PI * 2;
    const a1 = ((i + 1) / (teeth * 2)) * Math.PI * 2;
    const p0 = [Math.cos(a0) * r, Math.sin(a0) * r] as const;
    if (i === 0) shape.moveTo(p0[0], p0[1]);
    else shape.lineTo(p0[0], p0[1]);
    shape.lineTo(Math.cos(a1) * r, Math.sin(a1) * r);
  }
  const hole = new THREE.Path();
  hole.absarc(0, 0, 0.12, 0, Math.PI * 2, true);
  shape.holes.push(hole);
  cached = new THREE.ExtrudeGeometry(shape, { depth: 0.08, bevelEnabled: true, bevelThickness: 0.02, bevelSize: 0.015, bevelSegments: 2 });
  cached.center();
  return cached;
}
