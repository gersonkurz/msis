// Dependency-free unit test for the retain/cleanup core in ../hookcore.h.
//
// No test framework, no MSI, no wcautil, no NuGet: a plain console exe that compiles with
//   cl /std:c++17 /EHsc hookcore_test.cpp
// and returns 0 on success, non-zero on the first failure. It defines HookLogf (the diagnostic
// sink hookcore.h expects) as a capture stub so tests can assert on emitted messages too.

#include "../hookcore.h"

#include <windows.h>
#include <string>
#include <vector>
#include <set>
#include <cstdio>
#include <cstdarg>
#include <cstring>

// ---- HookLogf capture stub -------------------------------------------------
static std::vector<std::string> g_log;

void HookLogf(const char* fmt, ...)
{
    char buf[1024];
    va_list args;
    va_start(args, fmt);
    _vsnprintf_s(buf, sizeof(buf), _TRUNCATE, fmt, args);
    va_end(args);
    g_log.push_back(buf);
}

// ---- tiny assertion harness ------------------------------------------------
static int g_failures = 0;

#define CHECK(cond)                                                                     \
    do {                                                                                \
        if (!(cond)) {                                                                  \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);                 \
            ++g_failures;                                                               \
        }                                                                               \
    } while (0)

// ---- temp filesystem helpers ----------------------------------------------
static std::wstring MakeScratchRoot()
{
    wchar_t tmp[MAX_PATH];
    DWORD n = GetTempPathW(MAX_PATH, tmp);
    std::wstring root(tmp, n);
    // GetCurrentProcessId + GetTickCount give a unique-enough name without Date/rand.
    wchar_t suffix[64];
    swprintf_s(suffix, L"msis_hooktest_%lu_%lu", GetCurrentProcessId(), GetTickCount());
    root += suffix;
    CreateDirectoryW(root.c_str(), nullptr);
    return root;
}

static void MakeDir(const std::wstring& path)
{
    CreateDirectoryW(path.c_str(), nullptr);
}

static void MakeFile(const std::wstring& path, const char* contents = "x")
{
    HANDLE h = CreateFileW(path.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (h != INVALID_HANDLE_VALUE)
    {
        DWORD written = 0;
        WriteFile(h, contents, (DWORD)strlen(contents), &written, nullptr);
        CloseHandle(h);
    }
}

static bool Exists(const std::wstring& path)
{
    return GetFileAttributesW(path.c_str()) != INVALID_FILE_ATTRIBUTES;
}

static void RemoveScratch(const std::wstring& root)
{
    // best-effort cleanup of whatever the test left behind
    DeleteFolderRecursive(root, {});
}

// ===========================================================================
// ParseRetainList
// ===========================================================================
static void TestParseRetainList()
{
    // empty input -> empty set, no log noise
    g_log.clear();
    CHECK(ParseRetainList(L"").empty());

    // single entry, trimmed and lower-cased
    {
        auto s = ParseRetainList(L"  C:\\Data\\Foo.DB  ");
        CHECK(s.size() == 1);
        CHECK(s.count(L"c:\\data\\foo.db") == 1);
    }

    // multiple entries separated by ';'
    {
        auto s = ParseRetainList(L"C:\\a\\one.db;C:\\b\\two.ini");
        CHECK(s.size() == 2);
        CHECK(s.count(L"c:\\a\\one.db") == 1);
        CHECK(s.count(L"c:\\b\\two.ini") == 1);
    }

    // forward slashes normalized to backslashes
    {
        auto s = ParseRetainList(L"C:/data/sub/file.db");
        CHECK(s.count(L"c:\\data\\sub\\file.db") == 1);
    }

    // empty entries are warned about and skipped, not turned into ""
    {
        g_log.clear();
        auto s = ParseRetainList(L"C:\\a.db;;  ;C:\\b.db");
        CHECK(s.size() == 2);
        CHECK(s.count(L"") == 0);
        int warnings = 0;
        for (const auto& m : g_log)
            if (m.find("ignoring empty") != std::string::npos)
                ++warnings;
        CHECK(warnings == 2); // the "" and the "  " entries
    }
}

// ===========================================================================
// DeleteFolderRecursive — retain semantics
// ===========================================================================
static void TestDeleteWithRetain()
{
    const std::wstring root = MakeScratchRoot();

    // tree:
    //   root\app.exe                 (deleted)
    //   root\DATABASE\proakt.db      (RETAINED)
    //   root\DATABASE\scratch.tmp    (deleted)
    //   root\LOGS\old.log            (deleted -> LOGS empties -> removed)
    MakeFile(root + L"\\app.exe");
    MakeDir(root + L"\\DATABASE");
    MakeFile(root + L"\\DATABASE\\proakt.db");
    MakeFile(root + L"\\DATABASE\\scratch.tmp");
    MakeDir(root + L"\\LOGS");
    MakeFile(root + L"\\LOGS\\old.log");

    std::set<std::wstring> retain;
    retain.insert(ToLowerWide(root + L"\\DATABASE\\proakt.db"));

    DeleteFolderRecursive(root, retain);

    // retained file and its parent survive
    CHECK(Exists(root + L"\\DATABASE\\proakt.db"));
    CHECK(Exists(root + L"\\DATABASE"));
    // root itself survives because it transitively contains a retained file
    CHECK(Exists(root));

    // everything not retained is gone
    CHECK(!Exists(root + L"\\app.exe"));
    CHECK(!Exists(root + L"\\DATABASE\\scratch.tmp"));
    CHECK(!Exists(root + L"\\LOGS\\old.log"));
    CHECK(!Exists(root + L"\\LOGS"));

    RemoveScratch(root);
}

static void TestDeleteWithoutRetainRemovesEverything()
{
    const std::wstring root = MakeScratchRoot();

    MakeFile(root + L"\\a.exe");
    MakeDir(root + L"\\sub");
    MakeFile(root + L"\\sub\\b.dat");
    MakeDir(root + L"\\sub\\deep");
    MakeFile(root + L"\\sub\\deep\\c.dat");

    DeleteFolderRecursive(root, {});

    // empty retain set -> the whole tree is removed, root included
    CHECK(!Exists(root));
}

static void TestRetainPreservesNestedChain()
{
    const std::wstring root = MakeScratchRoot();

    // a deep retained file must preserve every ancestor up to root
    MakeDir(root + L"\\x");
    MakeDir(root + L"\\x\\y");
    MakeDir(root + L"\\x\\y\\z");
    MakeFile(root + L"\\x\\y\\z\\keep.cfg");
    MakeFile(root + L"\\x\\drop.txt");

    std::set<std::wstring> retain;
    retain.insert(ToLowerWide(root + L"\\x\\y\\z\\keep.cfg"));

    DeleteFolderRecursive(root, retain);

    CHECK(Exists(root + L"\\x\\y\\z\\keep.cfg"));
    CHECK(Exists(root + L"\\x\\y\\z"));
    CHECK(Exists(root + L"\\x\\y"));
    CHECK(Exists(root + L"\\x"));
    CHECK(!Exists(root + L"\\x\\drop.txt"));

    RemoveScratch(root);
}

static void TestTrailingSeparatorTolerated()
{
    const std::wstring root = MakeScratchRoot();
    MakeFile(root + L"\\f.dat");

    DeleteFolderRecursive(root + L"\\", {}); // trailing slash must not break removal

    CHECK(!Exists(root));
}

static void TestMissingFolderIsNoOp()
{
    const std::wstring root = MakeScratchRoot();
    const std::wstring missing = root + L"\\does-not-exist";

    DeleteFolderRecursive(missing, {}); // must not crash or throw

    CHECK(Exists(root)); // untouched
    RemoveScratch(root);
}

int main()
{
    TestParseRetainList();
    TestDeleteWithRetain();
    TestDeleteWithoutRetainRemovesEverything();
    TestRetainPreservesNestedChain();
    TestTrailingSeparatorTolerated();
    TestMissingFolderIsNoOp();

    if (g_failures == 0)
    {
        std::printf("hookcore_test: ALL TESTS PASSED\n");
        return 0;
    }
    std::printf("hookcore_test: %d CHECK(s) FAILED\n", g_failures);
    return 1;
}
