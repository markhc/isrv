import { hkdfSync } from "node:crypto"
import { afterEach, describe, expect, it, vi } from "vitest"
import {
  decryptContainer,
  E2EEAuthenticationError,
  E2EEFormatError,
  E2EEKeyError,
  E2EE_CHUNK_SIZE,
  encryptFile,
  maxEncryptedContainerSize,
  validateKeyToken
} from "./crypto"

function testFile(bytes: Uint8Array, name = "private.txt", type = "text/plain"): File {
  const contents = new Uint8Array(bytes.byteLength)
  contents.set(bytes)
  return new File([contents.buffer], name, { type })
}

function patternedBytes(length: number): Uint8Array<ArrayBuffer> {
  const bytes = new Uint8Array(length)
  for (let index = 0; index < bytes.length; index += 1) {
    bytes[index] = index % 251
  }
  return bytes
}

async function blobBytes(blob: Blob): Promise<Uint8Array<ArrayBuffer>> {
  return new Uint8Array(await blob.arrayBuffer())
}

function mutate(blob: Blob, offset: number): Promise<Blob> {
  return blob.arrayBuffer().then((buffer) => {
    const bytes = new Uint8Array(buffer)
    bytes[offset] ^= 0x01
    return new Blob([bytes], { type: blob.type })
  })
}

function decodeKeyToken(token: string): Uint8Array<ArrayBuffer> {
  const padding = "=".repeat((4 - token.length % 4) % 4)
  const binary = atob(token.replaceAll("-", "+").replaceAll("_", "/") + padding)
  return Uint8Array.from(binary, character => character.charCodeAt(0))
}

const SALT_OFFSET = 14
const KEY_COMMITMENT_OFFSET = 38
const HEADER_LENGTH = 70

async function sha256Hex(blob: Blob): Promise<string> {
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", await blob.arrayBuffer()))
  return Array.from(digest, byte => byte.toString(16).padStart(2, "0")).join("")
}

function stubDeterministicRandom(): void {
  let next = 0
  vi.spyOn(globalThis.crypto, "getRandomValues").mockImplementation(<T extends ArrayBufferView | null>(array: T): T => {
    if (array) {
      const bytes = new Uint8Array(array.buffer, array.byteOffset, array.byteLength)
      for (let index = 0; index < bytes.length; index += 1) {
        bytes[index] = next++ & 0xff
      }
    }
    return array
  })
}

describe("E2EE v1 crypto", () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it("round trips metadata and an empty file", async () => {
    const encrypted = await encryptFile(testFile(new Uint8Array()))
    const decrypted = await decryptContainer(encrypted.ciphertext, encrypted.keyToken)

    expect(decrypted.metadata).toEqual({
      name: "private.txt",
      type: "text/plain",
      size: 0,
    })
    expect(decrypted.plaintext.type).toBe("text/plain")
    expect(decrypted.plaintext.size).toBe(0)
  })

  it("round trips a file spanning multiple content records", async () => {
    const original = patternedBytes(E2EE_CHUNK_SIZE + 37)
    const encrypted = await encryptFile(testFile(original, "archive.bin", "application/x-test"))
    const decrypted = await decryptContainer(encrypted.ciphertext, encrypted.keyToken)

    expect(decrypted.metadata).toEqual({
      name: "archive.bin",
      type: "application/x-test",
      size: original.byteLength,
    })
    expect(await blobBytes(decrypted.plaintext)).toEqual(original)
  })

  it("uses an opaque key token with no textual version prefix", async () => {
    const encrypted = await encryptFile(testFile(new Uint8Array([1, 2, 3])))
    const tokenBytes = decodeKeyToken(encrypted.keyToken)

    expect(encrypted.keyToken).toMatch(/^[A-Za-z0-9_-]{44}$/)
    expect(encrypted.keyToken).not.toMatch(/^v1[.-]/)
    expect(tokenBytes).toHaveLength(33)
    expect(tokenBytes[0]).toBe(1)
  })

  it("does not expose private metadata in the ciphertext", async () => {
    const encrypted = await encryptFile(testFile(new Uint8Array([1, 2, 3]), "highly-secret-name.txt"))
    const text = new TextDecoder().decode(await encrypted.ciphertext.arrayBuffer())

    expect(text).not.toContain("highly-secret-name.txt")
    expect(text).not.toContain("text/plain")
  })

  it("rejects a wrong key", async () => {
    const first = await encryptFile(testFile(new Uint8Array([1, 2, 3])))
    const second = await encryptFile(testFile(new Uint8Array([4, 5, 6])))

    await expect(decryptContainer(first.ciphertext, second.keyToken))
      .rejects.toBeInstanceOf(E2EEAuthenticationError)
  })

  it("rejects header and content tampering", async () => {
    const encrypted = await encryptFile(testFile(new Uint8Array([1, 2, 3])))
    const tamperedHeader = await mutate(encrypted.ciphertext, 20)
    const tamperedContent = await mutate(encrypted.ciphertext, encrypted.ciphertext.size - 1)

    await expect(decryptContainer(tamperedHeader, encrypted.keyToken))
      .rejects.toBeInstanceOf(E2EEAuthenticationError)
    await expect(decryptContainer(tamperedContent, encrypted.keyToken))
      .rejects.toBeInstanceOf(E2EEAuthenticationError)
  })

  it("stores an HKDF key commitment in the header", async () => {
    const encrypted = await encryptFile(testFile(new Uint8Array([1, 2, 3])))
    const header = await blobBytes(encrypted.ciphertext.slice(0, HEADER_LENGTH))
    const rootKey = decodeKeyToken(encrypted.keyToken).slice(1)
    const salt = header.slice(SALT_OFFSET, SALT_OFFSET + 16)
    const expected = new Uint8Array(hkdfSync("sha256", rootKey, salt, "isrv-e2ee-v1/commit", 32))

    expect(header.slice(KEY_COMMITMENT_OFFSET, HEADER_LENGTH)).toEqual(expected)
  })

  it("rejects a key commitment mismatch before decrypting any record", async () => {
    const encrypted = await encryptFile(testFile(new Uint8Array([1, 2, 3])))
    const tampered = await mutate(encrypted.ciphertext, HEADER_LENGTH - 1)
    const decrypt = vi.spyOn(globalThis.crypto.subtle, "decrypt")

    await expect(decryptContainer(tampered, encrypted.keyToken))
      .rejects.toBeInstanceOf(E2EEAuthenticationError)
    expect(decrypt).not.toHaveBeenCalled()
  })

  it("matches the frozen v1 known-answer vector", async () => {
    stubDeterministicRandom()
    const encrypted = await encryptFile(testFile(patternedBytes(E2EE_CHUNK_SIZE + 5), "vector.bin", "application/octet-stream"))
    vi.restoreAllMocks()

    expect(encrypted.keyToken).toBe("AQABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f")
    expect(encrypted.ciphertext.size).toBe(HEADER_LENGTH + 4112 + E2EE_CHUNK_SIZE + 5 + 2 * 16)
    expect(await sha256Hex(encrypted.ciphertext)).toBe("e14900768f147eae5147c3f2b1cd4892b7c2128f7d94aeb3548d3a5cb5189510")

    const decrypted = await decryptContainer(encrypted.ciphertext, encrypted.keyToken)
    expect(await blobBytes(decrypted.plaintext)).toEqual(patternedBytes(E2EE_CHUNK_SIZE + 5))
  })

  it("rejects reordered content records", async () => {
    const empty = await encryptFile(testFile(new Uint8Array()))
    const encrypted = await encryptFile(testFile(patternedBytes(E2EE_CHUNK_SIZE * 2)))
    const contentOffset = empty.ciphertext.size
    const recordLength = (encrypted.ciphertext.size - contentOffset) / 2
    const reordered = new Blob([
      encrypted.ciphertext.slice(0, contentOffset),
      encrypted.ciphertext.slice(contentOffset + recordLength),
      encrypted.ciphertext.slice(contentOffset, contentOffset + recordLength),
    ])

    await expect(decryptContainer(reordered, encrypted.keyToken))
      .rejects.toBeInstanceOf(E2EEAuthenticationError)
  })

  it("rejects truncated and extended containers", async () => {
    const encrypted = await encryptFile(testFile(new Uint8Array([1, 2, 3])))
    const truncated = encrypted.ciphertext.slice(0, encrypted.ciphertext.size - 1)
    const extended = new Blob([encrypted.ciphertext, new Uint8Array([0])])

    await expect(decryptContainer(truncated, encrypted.keyToken))
      .rejects.toBeInstanceOf(E2EEFormatError)
    await expect(decryptContainer(extended, encrypted.keyToken))
      .rejects.toBeInstanceOf(E2EEFormatError)
  })

  it("rejects malformed key tokens", async () => {
    const encrypted = await encryptFile(testFile(new Uint8Array([1, 2, 3])))

    await expect(decryptContainer(encrypted.ciphertext, "not+a+key"))
      .rejects.toBeInstanceOf(E2EEKeyError)
  })

  it("validates key tokens before ciphertext is fetched", async () => {
    const encrypted = await encryptFile(testFile(new Uint8Array([1, 2, 3])))

    expect(() => validateKeyToken(encrypted.keyToken)).not.toThrow()
    expect(() => validateKeyToken("not+a+key")).toThrow(E2EEKeyError)
  })

  it("calculates the maximum v1 container size", () => {
    expect(maxEncryptedContainerSize(0)).toBe(70 + 4096 + 16)
    expect(maxEncryptedContainerSize(E2EE_CHUNK_SIZE)).toBe(
      70 + 4096 + 16 + E2EE_CHUNK_SIZE + 16
    )
  })
})
