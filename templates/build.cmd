@ECHO OFF

REM Application settings
SET WxsFilename=%1
SET MsiFilename=%2
SET Platform=%4

SET ProductName=%1
SET BuildTarget=mui-target
SET Culture=%3
SET MsiName=%ProductName%
SET LangIDs=1033

REM Development tool settings
SET WinSDKVersion=10
SET TEMPLATES_PATH=%~dp0
SET LOC="%TEMPLATES_PATH%\\wixlib\\%Culture%.wxl"

REM Build the MSI with WiX Toolkit
"wix.exe" build %WxsFilename% -ext WixToolset.UI.wixext -ext WixToolset.Util.wixext	-arch %Platform% -pdbtype none -loc %LOC% -culture %Culture% -o %BuildTarget%

COPY /Y "%BuildTarget%".msi %MsiFilename%
del /Q "%BuildTarget%".msi
 
SET ProductVersion=
SET Culture=
SET LangID=
SET LangIDs=
SET MsiName=
