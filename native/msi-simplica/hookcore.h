#pragma once
// Core retain/cleanup logic shared by the msi-simplica DLL and its unit test.
//
// This header is MSI-independent: it uses only Win32 + the C++ standard library, and emits
// diagnostics through HookLogf(), which each consumer defines (the DLL routes it to WcaLog; the
// unit test stubs/captures it). That lets the retain-aware delete and the retain-list parser be
// exercised by a plain console test with no MSI session, no wcautil, and no NuGet.
#include <windows.h>
#include <string>
#include <set>
#include <algorithm>

// printf-style diagnostic sink, defined by the consumer (DLL -> WcaLog; test -> stub/capture).
void HookLogf(const char* fmt, ...);

// Lower-case a wide string for case-insensitive path comparison.
inline std::wstring ToLowerWide(std::wstring s)
{
    std::transform(s.begin(), s.end(), s.begin(), ::towlower);
    return s;
}

// Parse an already-[PROPERTY]-resolved, ';'-separated retain list into a set of lower-cased paths.
// Entries are trimmed and '/'-normalized to '\'. Empty entries are warned about, not silently dropped.
inline std::set<std::wstring> ParseRetainList(const std::wstring& formatted)
{
    std::set<std::wstring> retain;
    if (formatted.empty())
        return retain;

    size_t start = 0;
    while (true)
    {
        const size_t sep = formatted.find(L';', start);
        std::wstring item = formatted.substr(start, sep == std::wstring::npos ? std::wstring::npos : sep - start);

        const size_t a = item.find_first_not_of(L" \t");
        if (a == std::wstring::npos)
        {
            HookLogf("MSIS-CA: WARNING: ignoring empty RETAIN_FILES_ON_UNINSTALL entry");
        }
        else
        {
            const size_t b = item.find_last_not_of(L" \t");
            std::wstring path = item.substr(a, b - a + 1);
            std::replace(path.begin(), path.end(), L'/', L'\\');
            HookLogf("MSIS-CA: will retain %S", path.c_str());
            retain.insert(ToLowerWide(path));
        }

        if (sep == std::wstring::npos)
            break;
        start = sep + 1;
    }
    return retain;
}

// Recursively delete the contents of 'folder', skipping any file whose lower-cased full path is in
// 'retain'. Returns true if anything was kept (the folder is preserved), false if the folder was
// emptied and removed. Parent folders of a retained file are preserved automatically because a kept
// child propagates "kept" upward. Undeletable files are also treated as kept (so we never try to
// remove a non-empty directory).
inline bool DeleteFolderKeepingRetained(const std::wstring& folder, const std::set<std::wstring>& retain)
{
    bool kept = false;
    WIN32_FIND_DATAW wfd;
    ZeroMemory(&wfd, sizeof(wfd));
    const std::wstring pattern(folder + L"\\*");

    HANDLE hFind = FindFirstFileW(pattern.c_str(), &wfd);
    if (INVALID_HANDLE_VALUE == hFind)
        return false; // folder missing or unreadable: nothing to remove

    do
    {
        if (wcscmp(wfd.cFileName, L".") == 0 || wcscmp(wfd.cFileName, L"..") == 0)
            continue;

        const std::wstring path(folder + L"\\" + wfd.cFileName);
        if (wfd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY)
        {
            if (DeleteFolderKeepingRetained(path, retain))
                kept = true;
        }
        else if (retain.find(ToLowerWide(path)) != retain.end())
        {
            HookLogf("MSIS-CA: retaining file %S", path.c_str());
            kept = true;
        }
        else
        {
            SetFileAttributesW(path.c_str(), FILE_ATTRIBUTE_NORMAL);
            if (DeleteFileW(path.c_str()))
            {
                HookLogf("MSIS-CA: deleted file %S", path.c_str());
            }
            else
            {
                HookLogf("MSIS-CA: ERROR %ld: could not delete file %S", GetLastError(), path.c_str());
                kept = true;
            }
        }
    } while (FindNextFileW(hFind, &wfd) != 0);
    FindClose(hFind);

    if (kept)
    {
        HookLogf("MSIS-CA: preserving folder %S (contains retained or undeletable items)", folder.c_str());
        return true;
    }

    SetFileAttributesW(folder.c_str(), FILE_ATTRIBUTE_NORMAL);
    if (RemoveDirectoryW(folder.c_str()))
    {
        HookLogf("MSIS-CA: deleted folder %S", folder.c_str());
        return false;
    }
    HookLogf("MSIS-CA: ERROR %ld: could not remove folder %S", GetLastError(), folder.c_str());
    return true;
}

// Public entry: strip trailing separators from the root, then delegate.
inline void DeleteFolderRecursive(const std::wstring& folderIn, const std::set<std::wstring>& retain)
{
    if (folderIn.empty())
        return;
    std::wstring root(folderIn);
    while (root.size() > 1 && (root.back() == L'\\' || root.back() == L'/'))
        root.pop_back();
    HookLogf("MSIS-CA: cleaning folder tree %S (%zu file(s) retained)", root.c_str(), retain.size());
    DeleteFolderKeepingRetained(root, retain);
}
