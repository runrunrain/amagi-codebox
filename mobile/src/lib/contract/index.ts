/**
 * Sole public export surface for the remote REST/WS v1 contract.
 *
 * Consumers MUST import from here:
 *   import type { ClientFrame, SessionDetail, KnownServerEvent } from '@/lib/contract';
 *
 * Do NOT deep-import scalars.ts/errors.ts/rest.ts/ws.ts. Re-exporting only
 * from index keeps the wire types in one place and prevents string drift.
 */
export * from './scalars';
export * from './errors';
export * from './rest';
export * from './ws';
