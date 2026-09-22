# Rebuilds unsigned.exe and signed.exe - the signed-bundle fixtures for internal/burnread.
#
# signed.exe is a bundle signed the way WiX supports: detach the engine, sign it, reattach it
# (which records where the engine's signature sits and so where the attached containers moved
# to), then sign the whole file. The certificate is self-signed, created here and removed from
# the store again; nothing trusts it and nothing needs to - the reader looks at the certificate
# table's presence and at the reattach record, never at who signed. unsigned.exe is the same
# build before the signing steps, so a test can show that signing changed nothing the reader
# reports.
#
# Run from this directory, with WiX 6 (`wix`) and the Windows SDK's signtool on the machine.
# Both outputs are then shrunk with `go run shrink.go`, like fixture.exe (see README.md).

$ErrorActionPreference = 'Stop'

$signtool = Get-ChildItem "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" |
    Sort-Object FullName -Descending | Select-Object -First 1 -ExpandProperty FullName
if (-not $signtool) { throw "signtool.exe not found under the Windows Kits; install the Windows SDK" }

wix build fixture.wxs -o unsigned.exe
if ($LASTEXITCODE -ne 0) { throw "wix build failed" }

$cert = New-SelfSignedCertificate -Type CodeSigningCert `
    -Subject 'CN=msis burnread fixture (self-signed, test only)' `
    -CertStoreLocation Cert:\CurrentUser\My -NotAfter (Get-Date).AddDays(2)
try {
    $password = ConvertTo-SecureString -String 'fixture' -Force -AsPlainText
    Export-PfxCertificate -Cert $cert -FilePath fixture-signing.pfx -Password $password | Out-Null

    wix burn detach unsigned.exe -engine engine.exe
    if ($LASTEXITCODE -ne 0) { throw "wix burn detach failed" }
    & $signtool sign /f fixture-signing.pfx /p fixture /fd SHA256 engine.exe
    if ($LASTEXITCODE -ne 0) { throw "signing the engine failed" }
    wix burn reattach unsigned.exe -engine engine.exe -o signed.exe
    if ($LASTEXITCODE -ne 0) { throw "wix burn reattach failed" }
    & $signtool sign /f fixture-signing.pfx /p fixture /fd SHA256 signed.exe
    if ($LASTEXITCODE -ne 0) { throw "signing the bundle failed" }
}
finally {
    Remove-Item -Path ("Cert:\CurrentUser\My\" + $cert.Thumbprint) -ErrorAction SilentlyContinue
    Remove-Item -Path fixture-signing.pfx, engine.exe -ErrorAction SilentlyContinue
}

# The subject appears in both signatures; the test TestAGenuinelySignedBundleIsSigned counts it.
$s = Get-AuthenticodeSignature signed.exe
Write-Host ("signed.exe: " + $s.Status + " (UnknownError is expected: nothing trusts the throwaway root) / " + $s.SignerCertificate.Subject)

go run shrink.go unsigned.exe
go run shrink.go signed.exe
