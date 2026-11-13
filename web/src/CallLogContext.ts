import { createContext, useContext } from 'react'

export type LogTarget = { id?: string; attemptId?: string; probeId?: string }
export const CallLogContext = createContext<((target: LogTarget) => void) | undefined>(undefined)
export const useCallLogs = () => useContext(CallLogContext)
