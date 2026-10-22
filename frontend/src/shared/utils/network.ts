export type NetworkInformationLike = { effectiveType?: string; saveData?: boolean; rtt?: number; downlink?: number }

export function networkInformation(): NetworkInformationLike | undefined {
  return (globalThis.navigator as unknown as { connection?: NetworkInformationLike } | undefined)?.connection
}

export function isSlowConnection(connection = networkInformation()): boolean {
  return !!connection && (connection.saveData === true
    || ['slow-2g', '2g', '3g'].includes(connection.effectiveType ?? '')
    || (typeof connection.rtt === 'number' && connection.rtt >= 400)
    || (typeof connection.downlink === 'number' && connection.downlink > 0 && connection.downlink < 1))
}
