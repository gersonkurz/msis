#include "stdafx.h"
#include "hookcore.h"
#include <cstdarg>
#include <filesystem>
#include <sstream>
#include <string>
#include <cstdlib>
#include <set>
#include <algorithm>

// hookcore.h emits diagnostics through HookLogf; route them to the WiX custom-action log.
void HookLogf(const char* fmt, ...)
{
    char buf[1024];
    va_list args;
    va_start(args, fmt);
    _vsnprintf_s(buf, sizeof(buf), _TRUNCATE, fmt, args);
    va_end(args);
    WcaLog(LOGMSG_STANDARD, "%s", buf);
}

static LPCWSTR KnownProperties[] = {
    L"INSTALLDIR",
    L"APPDATADIR",
    L"ACTION",
    L"ADDDEFAULT",
    L"ADDLOCAL",
    L"ADDSOURCE",
    L"ADVERTISE",
    L"AFTERREBOOT",
    L"ALLUSERS",
    L"ARPAUTHORIZEDCDFPREFIX",
    L"ARPCOMMENTS",
    L"ARPCONTACT",
    L"ARPHELPLINK",
    L"ARPHELPTELEPHONE",
    L"ARPINSTALLLOCATION",
    L"ARPNOMODIFY",
    L"ARPNOREMOVE",
    L"ARPNOREPAIR",
    L"ARPPRODUCTICON",
    L"ARPREADME",
    L"ARPSIZE",
    L"ARPSYSTEMCOMPONENT",
    L"ARPURLINFOABOUT",
    L"ARPURLUPDATEINFO",
    L"AVAILABLEFREEREG",
    L"AdminProperties",
    L"AdminToolsFolder",
    L"AdminUser",
    L"Alpha",
    L"AppDataFolder",
    L"BorderSide",
    L"BorderTop",
    L"CCP_DRIVE",
    L"COMPADDDEFAULT",
    L"COMPADDLOCAL",
    L"COMPADDSOURCE",
    L"COMPANYNAME",
    L"CaptionHeight",
    L"ColorBits",
    L"CommonAppDataFolder",
    L"CommonFiles64Folder",
    L"CommonFilesFolder",
    L"ComputerName",
    L"CostingComplete",
    L"CustomActionData",
    L"DISABLEADVTSHORTCUTS",
    L"DISABLEMEDIA",
    L"DISABLEROLLBACK",
    L"Date",
    L"DefaultUIFont",
    L"DesktopFolder",
    L"DiskPrompt",
    L"EXECUTEACTION",
    L"EXECUTEMODE",
    L"FASTOEM",
    L"FILEADDDEFAULT",
    L"FILEADDLOCAL",
    L"FILEADDSOURCE",
    L"FavoritesFolder",
    L"FontsFolder",
    L"INSTALLLEVEL",
    L"Installed",
    L"Intel",
    L"Intel64",
    L"IsAdminPackage",
    L"LIMITUI",
    L"LOGACTION",
    L"LeftUnit",
    L"LocalAppDataFolder",
    L"LogonUser",
    L"MEDIAPACKAGEPATH",
    L"MSIARPSETTINGSIDENTIFIER",
    L"MSICHECKCRCS",
    L"MSIDISABLEEEUI",
    L"MSIDISABLELUAPATCHING",
    L"MSIDISABLERMRESTART",
    L"MSIENFORCEUPGRADECOMPONENTRULES",
    L"MSIFASTINSTALL",
    L"MSIINSTALLPERUSER",
    L"MSIINSTANCEGUID",
    L"MSINEWINSTANCE",
    L"MSINODISABLEMEDIA",
    L"MSIPATCHREMOVE",
    L"MSIRESTARTMANAGERCONTROL",
    L"MSIRMSHUTDOWN",
    L"MSIUNINSTALLSUPERSEDEDCOMPONENTS",
    L"MSIUSEREALADMINDETECTION",
    L"Manufacturer",
    L"MediaSourceDir",
    L"MsiHiddenProperties",
    L"MsiLogFileLocation",
    L"MsiLogging",
    L"MsiNTProductType",
    L"MsiNTSuiteBackOffice",
    L"MsiNTSuiteDataCenter",
    L"MsiNTSuiteEnterprise",
    L"MsiNTSuitePersonal",
    L"MsiNTSuiteSmallBusiness",
    L"MsiNTSuiteSmallBusinessRestricted",
L"MsiNTSuiteWebServer",
L"MsiNetAssemblySupport",
L"MsiPatchRemovalList",
L"MsiRestartManagerSessionKey",
L"MsiRunningElevated",
L"MsiSystemRebootPending",
L"MsiTabletPC",
L"MsiUIHideCancel",
L"MsiUIProgressOnly",
L"MsiUISourceResOnly",
L"MsiWin32AssemblySupport",
L"Msix64",
L"MyPicturesFolder",
L"NOCOMPANYNAME",
L"NOUSERNAME",
L"NetHoodFolder",
L"OLEAdvtSupport",
L"OriginalDatabase",
L"OutOfDiskSpace",
L"OutOfNoRbDiskSpace",
L"PATCH",
L"PATCHNEWPACKAGECODE",
L"PATCHNEWSUMMARYCOMMENTS",
L"PATCHNEWSUMMARYSUBJECT",
L"PIDKEY",
L"PIDTemplate",
L"PRIMARYFOLDER",
L"PROMPTROLLBACKCOST",
L"ParentOriginalDatabase",
L"ParentProductCode",
L"PersonalFolder",
L"PhysicalMemory",
L"Preselected",
L"PrimaryVolumePath",
L"PrimaryVolumeSpaceAvailable",
L"PrimaryVolumeSpaceRemaining",
L"PrimaryVolumeSpaceRequired",
L"PrintHoodFolder",
L"Privileged",
L"ProductCode",
L"ProductID",
L"ProductLanguage",
L"ProductName",
L"ProductState",
L"ProductVersion",
L"ProgramFiles64Folder",
L"ProgramFilesFolder",
L"ProgramMenuFolder",
L"REBOOT",
L"REBOOTPROMPT",
L"REINSTALL",
L"REINSTALLMODE",
L"REMOVE",
L"REMOVE_REGISTRY_TREE",
L"RESUME",
L"ROOTDRIVE",
L"RecentFolder",
L"RedirectedDllSupport",
L"RemoteAdminTS",
L"ReplacedInUseFiles",
L"RollbackDisabled",
L"SEQUENCE",
L"SHORTFILENAMES",
L"ScreenX",
L"ScreenY",
L"SendToFolder",
L"ServicePackLevel",
L"ServicePackLevelMinor",
L"SharedWindows",
L"ShellAdvtSupport",
L"SourceDir",
L"StartMenuFolder",
L"StartupFolder",
L"System16Folder",
L"System64Folder",
L"SystemFolder",
L"SystemLanguageID",
L"TARGETDIR",
L"TRANSFORMS",
L"TRANSFORMSATSOURCE",
L"TRANSFORMSSECURE",
L"TTCSupport",
L"TempFolder",
L"TemplateFolder",
L"TerminalServer",
L"TextHeight",
L"Time",
L"UILevel",
L"UPGRADINGPRODUCTCODE",
L"USERNAME",
L"UpdateStarted",
L"UpgradeCode",
L"UserLanguageID",
L"Version9X",
L"VersionDatabase",
L"VersionMsi",
L"VersionNT",
L"VersionNT64",
L"VirtualMemory",
L"WindowsFolder",
L"WindowsVolume",
nullptr,
};

bool IsEqualString(LPCWSTR a, LPCWSTR b)
{
    return _wcsicmp(a, b) == 0;
}
void DeleteThisRegistryTree(HKEY hkParent, LPCWSTR lpszwName, LPCWSTR lpszwFolder)
{
    WcaLog(LOGMSG_STANDARD, "MSIS-CA: Delete registry-key %S in %S.", lpszwFolder, lpszwName);
    LONG lStatus = RegDeleteTree(hkParent, lpszwFolder);
    if (lStatus != ERROR_SUCCESS)
    {
        WcaLog(LOGMSG_STANDARD, "MSIS-CA: Delete registry-key failed with %ld", lStatus);
    }
}

void DeleteThisRegistryTree(LPCWSTR lpszwFolder)
{
    LPCWSTR lpszName = wcschr(lpszwFolder, L'\\');
    if (lpszName)
    {
        const std::wstring nameOfRoot(lpszwFolder, 0, lpszName - lpszwFolder);
        if (IsEqualString(nameOfRoot.c_str(), L"HKEY_LOCAL_MACHINE") ||
            IsEqualString(nameOfRoot.c_str(), L"HKLM"))
        {
            DeleteThisRegistryTree(HKEY_LOCAL_MACHINE, L"HKEY_LOCAL_MACHINE", lpszName + 1);
        }
        else if (IsEqualString(nameOfRoot.c_str(), L"HKEY_CLASSES_ROOT") ||
            IsEqualString(nameOfRoot.c_str(), L"HKCR"))
        {
            DeleteThisRegistryTree(HKEY_CLASSES_ROOT, L"HKEY_CLASSES_ROOT", lpszName + 1);
        }
        else if (IsEqualString(nameOfRoot.c_str(), L"HKEY_CURRENT_USER") ||
            IsEqualString(nameOfRoot.c_str(), L"HKCU"))
        {
            DeleteThisRegistryTree(HKEY_CURRENT_USER, L"HKEY_CURRENT_USER", lpszName + 1);
        }
        else
        {
            WcaLog(LOGMSG_STANDARD, "MSIS-CA: Don't know this key: %S", lpszwFolder);
        }
    }
}

void DeleteRegistryTreeRecursive(LPCWSTR lpszwFolder)
{
    if (!lpszwFolder || !*lpszwFolder)
        return;

    WcaLog(LOGMSG_STANDARD, "MSIS-CA: Delete registry-tree %S.", lpszwFolder);
    while (true)
    {
        LPCWSTR lpszNext = wcschr(lpszwFolder, L',');
        if (lpszNext)
        {
            DeleteThisRegistryTree(std::wstring(lpszwFolder, 0, lpszNext - lpszwFolder).c_str());
            lpszwFolder = lpszNext + 1;
        }
        else
        {
            DeleteThisRegistryTree(lpszwFolder);
            break;
        }
    }
}


// DeleteFolderRecursive / DeleteFolderKeepingRetained now live in hookcore.h (shared with the unit test).

std::wstring GetStringProperty(LPCWSTR lpszwName)
{
    std::wstring result;
    if (WcaIsUnicodePropertySet(lpszwName))
    {
        LPWSTR data = nullptr;

        HRESULT hResult = WcaGetProperty(lpszwName, &data);
        if (SUCCEEDED(hResult))
        {
            result = data;
            StrFree(data);
            WcaLog(LOGMSG_STANDARD, "MSIS-CA: '%S' = '%S'", lpszwName, result.c_str());
        }
        else
        {
            WcaLog(LOGMSG_STANDARD, "MSIS-CA: WcaGetProperty '%S' failed with %ld (0x%08x)", lpszwName, hResult, hResult);
        }
    }
    else
    {
        WcaLog(LOGMSG_STANDARD, "MSIS-CA: WcaIsUnicodePropertySet '%S' failed ", lpszwName);
    }
    return result;
}

// Resolve [PROPERTY] tokens in 'input' against the running MSI session (MsiFormatRecord).
static std::wstring FormatMsiString(MSIHANDLE hInstall, const std::wstring& input)
{
    if (input.empty())
        return input;
    PMSIHANDLE hRec = MsiCreateRecord(1);
    if (!hRec || MsiRecordSetString(hRec, 0, input.c_str()) != ERROR_SUCCESS)
        return input;

    WCHAR probe[1] = { 0 };
    DWORD cch = 0;
    if (MsiFormatRecord(hInstall, hRec, probe, &cch) != ERROR_MORE_DATA)
        return input;

    std::wstring buf(cch + 1, L'\0');
    DWORD cap = cch + 1;
    if (MsiFormatRecord(hInstall, hRec, &buf[0], &cap) != ERROR_SUCCESS)
        return input;
    buf.resize(cap);
    return buf;
}

// Build the retain set from RETAIN_FILES_ON_UNINSTALL: resolve [PROPERTY] tokens against the MSI
// session, then parse the ';'-list (see hookcore.h::ParseRetainList).
static std::set<std::wstring> BuildRetainSet(MSIHANDLE hInstall)
{
    const std::wstring raw = GetStringProperty(L"RETAIN_FILES_ON_UNINSTALL");
    if (raw.empty())
        return {};
    return ParseRetainList(FormatMsiString(hInstall, raw));
}

inline bool IsRemoveAll()
{
    return _wcsicmp(GetStringProperty(L"REMOVE").c_str(), L"ALL") == 0;
}

UINT __stdcall RemoveAllFoldersOnUninstall(MSIHANDLE hInstall)
{
    HRESULT hr = S_OK;
    UINT er = ERROR_SUCCESS;

    hr = WcaInitialize(hInstall, "RemoveAllFoldersOnUninstall");
    ExitOnFailure(hr, "Failed to initialize");

    WcaLog(LOGMSG_STANDARD, "MSIS-CA: BEGIN RemoveAllFoldersOnUninstall");

    WcaLog(LOGMSG_STANDARD, "MSIS-CA: REMOVE_FOLDERS_ON_UNINSTALL=%s", GetStringProperty(L"REMOVE_FOLDERS_ON_UNINSTALL").c_str());

    if (IsRemoveAll())
    {
        const std::set<std::wstring> retain = BuildRetainSet(hInstall);
        DeleteFolderRecursive(GetStringProperty(L"INSTALLDIR"), retain);
        DeleteFolderRecursive(GetStringProperty(L"APPDATADIR"), retain);
    }

    WcaLog(LOGMSG_STANDARD, "MSIS-CA: END RemoveAllFoldersOnUninstall");
LExit:
    er = SUCCEEDED(hr) ? ERROR_SUCCESS : ERROR_INSTALL_FAILURE;
    return WcaFinalize(er);
}

UINT __stdcall RemoveRegistryTreeOnUninstall(MSIHANDLE hInstall)
{
    HRESULT hr = S_OK;
    UINT er = ERROR_SUCCESS;

    hr = WcaInitialize(hInstall, "RemoveRegistryTreeOnUninstall");
    ExitOnFailure(hr, "Failed to initialize");

    WcaLog(LOGMSG_STANDARD, "MSIS-CA: BEGIN RemoveRegistryTreeOnUninstall");

    DeleteRegistryTreeRecursive(GetStringProperty(L"CustomActionData").c_str());
    WcaLog(LOGMSG_STANDARD, "MSIS-CA: END RemoveRegistryTreeOnUninstall");
LExit:
    er = SUCCEEDED(hr) ? ERROR_SUCCESS : ERROR_INSTALL_FAILURE;
    return WcaFinalize(er);
}


UINT __stdcall ListAllKnownProperties(MSIHANDLE hInstall)
{
    HRESULT hr = S_OK;
    UINT er = ERROR_SUCCESS;

    hr = WcaInitialize(hInstall, "ListAllKnownProperties");
    ExitOnFailure(hr, "Failed to initialize");

    WcaLog(LOGMSG_STANDARD, "MSIS-CA: BEGIN ListAllKnownProperties");

    for (DWORD dwIndex = 0; KnownProperties[dwIndex]; ++dwIndex)
    {
        LPWSTR data = nullptr;
        int idata = 0;

        HRESULT hResult = WcaGetProperty(
            KnownProperties[dwIndex],
            &data);

        if (SUCCEEDED(hResult))
        {
            if (data && *data)
            {
                WcaLog(LOGMSG_STANDARD, "MSIS-CA: %S='%S'",
                    KnownProperties[dwIndex], data);
            }
            else
            {
                WcaLog(LOGMSG_STANDARD, "MSIS-CA: %S=<null or empty>",
                    KnownProperties[dwIndex]);
            }

            StrFree(data);
        }

        else if (SUCCEEDED(WcaGetIntProperty(KnownProperties[dwIndex], &idata)))
        {
            WcaLog(LOGMSG_STANDARD, "MSIS-CA: %S=%d",
                KnownProperties[dwIndex], data);
        }
        else
        {
            WcaLog(LOGMSG_STANDARD, "MSIS-CA: %S=<unkown>",
                KnownProperties[dwIndex]);
        }
    }

    WcaLog(LOGMSG_STANDARD, "MSIS-CA: END ListAllKnownProperties");
LExit:
    er = SUCCEEDED(hr) ? ERROR_SUCCESS : ERROR_INSTALL_FAILURE;
    return WcaFinalize(er);
}


// DllMain - Initialize and cleanup WiX custom action utils.
extern "C" BOOL WINAPI DllMain(
    __in HINSTANCE hInst,
    __in ULONG ulReason,
    __in LPVOID
)
{
    switch (ulReason)
    {
    case DLL_PROCESS_ATTACH:
        WcaGlobalInitialize(hInst);
        break;

    case DLL_PROCESS_DETACH:
        WcaGlobalFinalize();
        break;
    }

    return TRUE;
}
