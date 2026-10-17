const MAGIC = new TextEncoder().encode("ISRV-E2E")
export const E2EE_VERSION = 1
export const E2EE_UPLOAD_FILENAME = "encrypted-file.isrv"
const KEY_TOKEN_LENGTH = 33
const ROOT_KEY_LENGTH = 32
const SALT_LENGTH = 16
const NONCE_PREFIX_LENGTH = 8
const KEY_COMMITMENT_LENGTH = 32
const NONCE_LENGTH = 12
const TAG_LENGTH_BYTES = 16
const METADATA_PLAINTEXT_LENGTH = 4096
const METADATA_CIPHERTEXT_LENGTH = METADATA_PLAINTEXT_LENGTH + TAG_LENGTH_BYTES

export const E2EE_CHUNK_SIZE = 1024 * 1024

// v1 container, all integer fields big-endian:
//
//   magic[8] | version[1] | flags[1] | chunk_size[4] |
//   hkdf_salt[16] | nonce_prefix[8] | key_commitment[32] |
//   encrypted_metadata[4112] | encrypted_content_records...
//
// key_commitment is a third HKDF output. AES-GCM is not key-committing, so
// without it a crafted container could authenticate under two keys and
// decrypt to different content for each. The whole header, commitment
// included, is additional data for every record.
//
// Metadata plaintext is a uint32 JSON length, UTF-8 JSON, then random padding
// to 4096 bytes. Content record boundaries are derived from the authenticated
// plaintext size in that metadata. Each record adds a 16-byte AES-GCM tag.
const HEADER_LENGTH = MAGIC.length + 1 + 1 + 4 + SALT_LENGTH + NONCE_PREFIX_LENGTH + KEY_COMMITMENT_LENGTH

const VERSION_OFFSET = MAGIC.length
const FLAGS_OFFSET = VERSION_OFFSET + 1
const CHUNK_SIZE_OFFSET = FLAGS_OFFSET + 1
const SALT_OFFSET = CHUNK_SIZE_OFFSET + 4
const NONCE_PREFIX_OFFSET = SALT_OFFSET + SALT_LENGTH
const KEY_COMMITMENT_OFFSET = NONCE_PREFIX_OFFSET + NONCE_PREFIX_LENGTH

const RECORD_TYPE_METADATA = 1
const RECORD_TYPE_CONTENT = 2
const METADATA_INFO = new TextEncoder().encode("isrv-e2ee-v1/metadata")
const CONTENT_INFO = new TextEncoder().encode("isrv-e2ee-v1/content")
const COMMIT_INFO = new TextEncoder().encode("isrv-e2ee-v1/commit")

export interface E2EEMetadata {
  name: string
  type: string
  size: number
}

export interface EncryptedContainer {
  ciphertext: Blob
  keyToken: string
}

export interface DecryptedContainer {
  plaintext: Blob
  metadata: E2EEMetadata
}

export class E2EEUnavailableError extends Error {
  constructor() {
    super("end-to-end encryption requires the Web Crypto API")
    this.name = "E2EEUnavailableError"
  }
}

export class E2EEKeyError extends Error {
  constructor(message: string) {
    super(message)
    this.name = "E2EEKeyError"
  }
}

export class E2EEFormatError extends Error {
  constructor(message: string) {
    super(message)
    this.name = "E2EEFormatError"
  }
}

export class E2EEAuthenticationError extends Error {
  constructor(cause?: unknown) {
    super("wrong key or damaged file", { cause })
    this.name = "E2EEAuthenticationError"
  }
}

export function isE2EESupported(): boolean {
  return globalThis.isSecureContext === true && globalThis.crypto?.subtle != null
}

export function validateKeyToken(keyToken: string): void {
  const decoded = decodeKeyToken(keyToken)
  decoded.rootKey.fill(0)
}

export function maxEncryptedContainerSize(maxPlaintextSize: number): number {
  assertSupportedSize(maxPlaintextSize)
  return HEADER_LENGTH
    + METADATA_CIPHERTEXT_LENGTH
    + maxPlaintextSize
    + contentRecordCount(maxPlaintextSize) * TAG_LENGTH_BYTES
}

export async function encryptFile(file: File): Promise<EncryptedContainer> {
  assertSupportedSize(file.size)

  const crypto = getCrypto()
  const rootKey = randomBytes(ROOT_KEY_LENGTH, crypto)
  const keyToken = encodeKeyToken(rootKey)
  const salt = randomBytes(SALT_LENGTH, crypto)
  const noncePrefix = randomBytes(NONCE_PREFIX_LENGTH, crypto)

  let keys: DerivedKeys
  try {
    keys = await deriveKeys(rootKey, salt, "encrypt", crypto)
  } finally {
    rootKey.fill(0)
  }

  const header = buildHeader(salt, noncePrefix, keys.commitment)

  const metadata = encodeMetadata({
    name: file.name,
    type: file.type,
    size: file.size,
  }, crypto)

  let encryptedMetadata: ArrayBuffer
  try {
    encryptedMetadata = await crypto.subtle.encrypt(
      aesGCMParams(header, noncePrefix, RECORD_TYPE_METADATA, 0, metadata.byteLength),
      keys.metadata,
      metadata
    )
  } finally {
    metadata.fill(0)
  }

  const parts: BlobPart[] = [header, encryptedMetadata]
  const records = contentRecordCount(file.size)

  for (let index = 0; index < records; index += 1) {
    const start = index * E2EE_CHUNK_SIZE
    const end = Math.min(start + E2EE_CHUNK_SIZE, file.size)
    const plaintext = new Uint8Array(await file.slice(start, end).arrayBuffer())
    const counter = index + 1
    let ciphertext: ArrayBuffer
    try {
      ciphertext = await crypto.subtle.encrypt(
        aesGCMParams(header, noncePrefix, RECORD_TYPE_CONTENT, counter, plaintext.byteLength),
        keys.content,
        plaintext
      )
    } finally {
      plaintext.fill(0)
    }
    parts.push(ciphertext)
  }

  return {
    ciphertext: new Blob(parts, { type: "application/octet-stream" }),
    keyToken,
  }
}

export async function decryptContainer(
  ciphertext: Blob,
  keyToken: string
): Promise<DecryptedContainer> {
  if (ciphertext.size < HEADER_LENGTH + METADATA_CIPHERTEXT_LENGTH) {
    throw new E2EEFormatError("encrypted file is too short")
  }

  const crypto = getCrypto()
  const token = decodeKeyToken(keyToken)
  const header = new Uint8Array(await ciphertext.slice(0, HEADER_LENGTH).arrayBuffer())
  const parsedHeader = parseHeader(header)

  if (token.version !== parsedHeader.version) {
    token.rootKey.fill(0)
    throw new E2EEKeyError("key version does not match the encrypted file")
  }

  let keys: DerivedKeys
  try {
    keys = await deriveKeys(token.rootKey, parsedHeader.salt, "decrypt", crypto)
  } finally {
    token.rootKey.fill(0)
  }

  if (!constantTimeEqual(keys.commitment, parsedHeader.keyCommitment)) {
    throw new E2EEAuthenticationError()
  }

  const metadataStart = HEADER_LENGTH
  const metadataEnd = metadataStart + METADATA_CIPHERTEXT_LENGTH
  const encryptedMetadata = await ciphertext.slice(metadataStart, metadataEnd).arrayBuffer()

  let metadataPlaintext: ArrayBuffer
  try {
    metadataPlaintext = await crypto.subtle.decrypt(
      aesGCMParams(
        header,
        parsedHeader.noncePrefix,
        RECORD_TYPE_METADATA,
        0,
        METADATA_PLAINTEXT_LENGTH
      ),
      keys.metadata,
      encryptedMetadata
    )
  } catch (error) {
    throw new E2EEAuthenticationError(error)
  }

  const metadataBytes = new Uint8Array(metadataPlaintext)
  let metadata: E2EEMetadata
  try {
    metadata = decodeMetadata(metadataBytes)
  } finally {
    metadataBytes.fill(0)
  }
  assertSupportedSize(metadata.size)

  const records = contentRecordCount(metadata.size)
  const expectedSize = metadataEnd + metadata.size + records * TAG_LENGTH_BYTES
  if (ciphertext.size !== expectedSize) {
    throw new E2EEFormatError("encrypted file length does not match its metadata")
  }

  const parts: Uint8Array<ArrayBuffer>[] = []
  let offset = metadataEnd

  let plaintext: Blob
  try {
    for (let index = 0; index < records; index += 1) {
      const plaintextLength = Math.min(E2EE_CHUNK_SIZE, metadata.size - index * E2EE_CHUNK_SIZE)
      const ciphertextLength = plaintextLength + TAG_LENGTH_BYTES
      const encryptedRecord = await ciphertext.slice(offset, offset + ciphertextLength).arrayBuffer()
      const counter = index + 1

      try {
        const record = await crypto.subtle.decrypt(
          aesGCMParams(
            header,
            parsedHeader.noncePrefix,
            RECORD_TYPE_CONTENT,
            counter,
            plaintextLength
          ),
          keys.content,
          encryptedRecord
        )
        parts.push(new Uint8Array(record))
      } catch (error) {
        throw new E2EEAuthenticationError(error)
      }

      offset += ciphertextLength
    }

    plaintext = new Blob(parts, { type: metadata.type || "application/octet-stream" })
  } finally {
    for (const part of parts) {
      part.fill(0)
    }
  }

  return {
    plaintext,
    metadata,
  }
}

interface DecodedKeyToken {
  version: number
  rootKey: Uint8Array<ArrayBuffer>
}

interface ParsedHeader {
  version: number
  salt: Uint8Array<ArrayBuffer>
  noncePrefix: Uint8Array<ArrayBuffer>
  keyCommitment: Uint8Array<ArrayBuffer>
}

interface DerivedKeys {
  metadata: CryptoKey
  content: CryptoKey
  commitment: Uint8Array<ArrayBuffer>
}

function getCrypto(): Crypto {
  if (!globalThis.crypto?.subtle) {
    throw new E2EEUnavailableError()
  }
  return globalThis.crypto
}

function randomBytes(length: number, crypto: Crypto): Uint8Array<ArrayBuffer> {
  return crypto.getRandomValues(new Uint8Array(length))
}

function encodeKeyToken(rootKey: Uint8Array): string {
  if (rootKey.byteLength !== ROOT_KEY_LENGTH) {
    throw new E2EEKeyError("invalid root key length")
  }

  const bytes = new Uint8Array(KEY_TOKEN_LENGTH)
  bytes[0] = E2EE_VERSION
  bytes.set(rootKey, 1)
  return bytesToBase64URL(bytes)
}

function decodeKeyToken(token: string): DecodedKeyToken {
  if (!/^[A-Za-z0-9_-]+$/.test(token)) {
    throw new E2EEKeyError("key is not canonical base64url")
  }

  let bytes: Uint8Array<ArrayBuffer>
  try {
    bytes = base64URLToBytes(token)
  } catch (error) {
    throw new E2EEKeyError(`key is not valid base64url: ${String(error)}`)
  }

  if (bytes.byteLength !== KEY_TOKEN_LENGTH || bytesToBase64URL(bytes) !== token) {
    throw new E2EEKeyError("invalid key length or encoding")
  }
  if (bytes[0] !== E2EE_VERSION) {
    throw new E2EEKeyError(`unsupported key version: ${bytes[0]}`)
  }

  return {
    version: bytes[0],
    rootKey: bytes.slice(1),
  }
}

function bytesToBase64URL(bytes: Uint8Array): string {
  let binary = ""
  for (const byte of bytes) {
    binary += String.fromCharCode(byte)
  }
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "")
}

function base64URLToBytes(value: string): Uint8Array<ArrayBuffer> {
  const padding = "=".repeat((4 - value.length % 4) % 4)
  const binary = atob(value.replaceAll("-", "+").replaceAll("_", "/") + padding)
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index)
  }
  return bytes
}

function buildHeader(
  salt: Uint8Array,
  noncePrefix: Uint8Array,
  keyCommitment: Uint8Array
): Uint8Array<ArrayBuffer> {
  const header = new Uint8Array(HEADER_LENGTH)
  header.set(MAGIC, 0)
  header[VERSION_OFFSET] = E2EE_VERSION
  header[FLAGS_OFFSET] = 0
  new DataView(header.buffer).setUint32(CHUNK_SIZE_OFFSET, E2EE_CHUNK_SIZE, false)
  header.set(salt, SALT_OFFSET)
  header.set(noncePrefix, NONCE_PREFIX_OFFSET)
  header.set(keyCommitment, KEY_COMMITMENT_OFFSET)
  return header
}

function parseHeader(header: Uint8Array): ParsedHeader {
  if (header.byteLength !== HEADER_LENGTH) {
    throw new E2EEFormatError("invalid encrypted file header length")
  }

  for (let index = 0; index < MAGIC.length; index += 1) {
    if (header[index] !== MAGIC[index]) {
      throw new E2EEFormatError("encrypted file magic is invalid")
    }
  }

  const version = header[VERSION_OFFSET]
  if (version !== E2EE_VERSION) {
    throw new E2EEFormatError(`unsupported encrypted file version: ${version}`)
  }
  if (header[FLAGS_OFFSET] !== 0) {
    throw new E2EEFormatError("encrypted file uses unsupported flags")
  }

  const chunkSize = new DataView(
    header.buffer,
    header.byteOffset,
    header.byteLength
  ).getUint32(CHUNK_SIZE_OFFSET, false)
  if (chunkSize !== E2EE_CHUNK_SIZE) {
    throw new E2EEFormatError("encrypted file uses an unsupported chunk size")
  }

  return {
    version,
    salt: copyBytes(header.subarray(SALT_OFFSET, NONCE_PREFIX_OFFSET)),
    noncePrefix: copyBytes(header.subarray(NONCE_PREFIX_OFFSET, KEY_COMMITMENT_OFFSET)),
    keyCommitment: copyBytes(header.subarray(KEY_COMMITMENT_OFFSET, HEADER_LENGTH)),
  }
}

async function deriveKeys(
  rootKey: Uint8Array,
  salt: Uint8Array,
  usage: "encrypt" | "decrypt",
  crypto: Crypto
): Promise<DerivedKeys> {
  const keyMaterial = await crypto.subtle.importKey(
    "raw",
    copyBytes(rootKey),
    "HKDF",
    false,
    ["deriveKey", "deriveBits"]
  )

  const hkdfParams = (info: Uint8Array): HkdfParams => ({
    name: "HKDF",
    hash: "SHA-256",
    salt: copyBytes(salt),
    info: copyBytes(info),
  })

  const derive = (info: Uint8Array) => crypto.subtle.deriveKey(
    hkdfParams(info),
    keyMaterial,
    { name: "AES-GCM", length: 256 },
    false,
    [usage]
  )

  const [metadata, content, commitment] = await Promise.all([
    derive(METADATA_INFO),
    derive(CONTENT_INFO),
    crypto.subtle.deriveBits(hkdfParams(COMMIT_INFO), keyMaterial, KEY_COMMITMENT_LENGTH * 8),
  ])
  return { metadata, content, commitment: new Uint8Array(commitment) }
}

function constantTimeEqual(a: Uint8Array, b: Uint8Array): boolean {
  if (a.byteLength !== b.byteLength) {
    return false
  }

  let difference = 0
  for (let index = 0; index < a.byteLength; index += 1) {
    difference |= a[index] ^ b[index]
  }
  return difference === 0
}

function encodeMetadata(metadata: E2EEMetadata, crypto: Crypto): Uint8Array<ArrayBuffer> {
  validateMetadata(metadata)

  const json = new TextEncoder().encode(JSON.stringify(metadata))
  if (json.byteLength > METADATA_PLAINTEXT_LENGTH - 4) {
    throw new E2EEFormatError("file metadata is too large")
  }

  const padded = randomBytes(METADATA_PLAINTEXT_LENGTH, crypto)
  new DataView(padded.buffer).setUint32(0, json.byteLength, false)
  padded.set(json, 4)
  return padded
}

function decodeMetadata(padded: Uint8Array): E2EEMetadata {
  if (padded.byteLength !== METADATA_PLAINTEXT_LENGTH) {
    throw new E2EEFormatError("invalid metadata length")
  }

  const jsonLength = new DataView(
    padded.buffer,
    padded.byteOffset,
    padded.byteLength
  ).getUint32(0, false)
  if (jsonLength > METADATA_PLAINTEXT_LENGTH - 4) {
    throw new E2EEFormatError("invalid metadata payload length")
  }

  let value: unknown
  try {
    value = JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(padded.subarray(4, 4 + jsonLength)))
  } catch (error) {
    throw new E2EEFormatError(`invalid encrypted metadata: ${String(error)}`)
  }

  validateMetadata(value)
  return value
}

function validateMetadata(value: unknown): asserts value is E2EEMetadata {
  if (typeof value !== "object" || value === null) {
    throw new E2EEFormatError("metadata must be an object")
  }

  const metadata = value as Partial<E2EEMetadata>
  if (typeof metadata.name !== "string" || typeof metadata.type !== "string") {
    throw new E2EEFormatError("metadata name and type must be strings")
  }
  if (!Number.isSafeInteger(metadata.size) || (metadata.size ?? -1) < 0) {
    throw new E2EEFormatError("metadata size must be a non-negative safe integer")
  }
}

function aesGCMParams(
  header: Uint8Array,
  noncePrefix: Uint8Array,
  recordType: number,
  counter: number,
  plaintextLength: number
): AesGcmParams {
  return {
    name: "AES-GCM",
    iv: makeNonce(noncePrefix, counter),
    additionalData: makeAdditionalData(header, recordType, counter, plaintextLength),
    tagLength: TAG_LENGTH_BYTES * 8,
  }
}

function makeNonce(noncePrefix: Uint8Array, counter: number): Uint8Array<ArrayBuffer> {
  if (noncePrefix.byteLength !== NONCE_PREFIX_LENGTH) {
    throw new E2EEFormatError("invalid nonce prefix length")
  }
  if (!Number.isInteger(counter) || counter < 0 || counter > 0xffffffff) {
    throw new E2EEFormatError("record counter is out of range")
  }

  const nonce = new Uint8Array(NONCE_LENGTH)
  nonce.set(noncePrefix, 0)
  new DataView(nonce.buffer).setUint32(NONCE_PREFIX_LENGTH, counter, false)
  return nonce
}

function makeAdditionalData(
  header: Uint8Array,
  recordType: number,
  counter: number,
  plaintextLength: number
): Uint8Array<ArrayBuffer> {
  const additionalData = new Uint8Array(header.byteLength + 1 + 4 + 4)
  additionalData.set(header, 0)
  additionalData[header.byteLength] = recordType
  const view = new DataView(additionalData.buffer)
  view.setUint32(header.byteLength + 1, counter, false)
  view.setUint32(header.byteLength + 5, plaintextLength, false)
  return additionalData
}

function contentRecordCount(size: number): number {
  const count = Math.ceil(size / E2EE_CHUNK_SIZE)
  if (count > 0xffffffff) {
    throw new E2EEFormatError("file is too large for the v1 record counter")
  }
  return count
}

function assertSupportedSize(size: number): void {
  if (!Number.isSafeInteger(size) || size < 0) {
    throw new E2EEFormatError("file size must be a non-negative safe integer")
  }
  contentRecordCount(size)
}

function copyBytes(bytes: Uint8Array): Uint8Array<ArrayBuffer> {
  const copy = new Uint8Array(bytes.byteLength)
  copy.set(bytes)
  return copy
}
