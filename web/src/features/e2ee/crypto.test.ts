import { describe, expect, it } from "vitest"
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

describe("E2EE v1 crypto", () => {
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
    expect(maxEncryptedContainerSize(0)).toBe(38 + 4096 + 16)
    expect(maxEncryptedContainerSize(E2EE_CHUNK_SIZE)).toBe(
      38 + 4096 + 16 + E2EE_CHUNK_SIZE + 16
    )
  })
})
