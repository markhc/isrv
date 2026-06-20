export interface ServerConfig {
  serverName: string
  disableUploadPage: boolean
  maxFileSizeMb: number
  minAgeDays: number
  maxAgeDays: number
  version: string
}

declare global {
  interface Window {
    __ISRV_CONFIG__?: ServerConfig
  }
}

export function getServerConfig(): ServerConfig {
  return window.__ISRV_CONFIG__ || {
    serverName: "iSRV",
    disableUploadPage: false,
    maxFileSizeMb: 100,
    minAgeDays: 1,
    maxAgeDays: 30,
    version: "0.0.0-dev",
  }
}
