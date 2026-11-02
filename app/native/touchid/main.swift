// sshgate-touchid: seals the vault's master password to a Secure Enclave key
// that only Touch ID opens (spec 2026-10-12-touchid-unlock-design.md). It never
// touches the disk: Electron main keeps the sealed JSON in touchid.json.
// Exit: 0 ok, 1 other error, 2 cancelled, 3 key no longer works (fingerprints
// changed), 4 Touch ID not usable now.
import CryptoKit
import CryptoTokenKit
import Foundation
import LocalAuthentication

struct Sealed: Codable { let v: Int; let key: String; let eph: String; let ct: String }

func fail(_ code: Int32, _ msg: String = "") -> Never {
  if !msg.isEmpty { FileHandle.standardError.write(Data((msg + "\n").utf8)) }
  exit(code)
}

func boxKey(_ s: SharedSecret) -> SymmetricKey {
  s.hkdfDerivedSymmetricKey(using: SHA256.self, salt: Data(), sharedInfo: Data("sshgate touchid v1".utf8), outputByteCount: 32)
}

func available() -> Bool {
  var err: NSError?
  return SecureEnclave.isAvailable && LAContext().canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &err)
}

// nil: not a cancel or an availability error, so the caller picks the code.
func knownCode(_ e: Error) -> Int32? {
  if let la = e as? LAError {
    switch la.code {
    case .userCancel, .appCancel, .systemCancel, .userFallback: return 2
    case .biometryNotAvailable, .biometryNotEnrolled, .biometryLockout: return 4
    default: return 1
    }
  }
  let ns = e as NSError
  if ns.domain == TKError.errorDomain && ns.code == TKError.Code.canceledByUser.rawValue { return 2 }
  return nil
}

let args = CommandLine.arguments
guard args.count == 2 else { fail(1, "usage: sshgate-touchid available|enroll|unlock") }

switch args[1] {
case "available":
  exit(available() ? 0 : 4)

case "enroll":
  guard available() else { fail(4) }
  let pw = FileHandle.standardInput.readDataToEndOfFile()
  guard !pw.isEmpty else { fail(1, "empty password") }
  guard let ac = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleWhenUnlockedThisDeviceOnly, [.privateKeyUsage, .biometryCurrentSet], nil) else {
    fail(1, "access control")
  }
  do {
    let key = try SecureEnclave.P256.KeyAgreement.PrivateKey(accessControl: ac)
    let eph = P256.KeyAgreement.PrivateKey()
    let box = try AES.GCM.seal(pw, using: boxKey(try eph.sharedSecretFromKeyAgreement(with: key.publicKey)))
    let out = Sealed(v: 1, key: key.dataRepresentation.base64EncodedString(),
                     eph: eph.publicKey.rawRepresentation.base64EncodedString(), ct: box.combined!.base64EncodedString())
    FileHandle.standardOutput.write(try JSONEncoder().encode(out))
  } catch { fail(knownCode(error) ?? 1, "enroll: \(String(reflecting: error))") }

case "unlock":
  guard available() else { fail(4) }
  guard let s = try? JSONDecoder().decode(Sealed.self, from: FileHandle.standardInput.readDataToEndOfFile()), s.v == 1,
        let keyData = Data(base64Encoded: s.key), let ephData = Data(base64Encoded: s.eph), let ct = Data(base64Encoded: s.ct),
        let eph = try? P256.KeyAgreement.PublicKey(rawRepresentation: ephData),
        let box = try? AES.GCM.SealedBox(combined: ct) else { fail(1, "bad input") }
  let ctx = LAContext()
  ctx.localizedReason = "unlock the sshgate vault"
  ctx.localizedFallbackTitle = "" // no "Use Password…": the app's own password field is the fallback
  let secret: SharedSecret
  do {
    let key = try SecureEnclave.P256.KeyAgreement.PrivateKey(dataRepresentation: keyData, authenticationContext: ctx)
    secret = try key.sharedSecretFromKeyAgreement(with: eph) // shows the Touch ID dialog
  } catch { fail(knownCode(error) ?? 3, "unlock: \(String(reflecting: error))") }
  guard let pw = try? AES.GCM.open(box, using: boxKey(secret)) else { fail(1, "open failed") }
  FileHandle.standardOutput.write(pw)

default:
  fail(1, "usage: sshgate-touchid available|enroll|unlock")
}
