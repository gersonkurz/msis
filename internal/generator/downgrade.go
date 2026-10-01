package generator

import (
	"fmt"
	"strconv"
	"strings"
)

// The downgrade guard (#89, decisions D27).
//
// Windows Installer compares only the first three fields of ProductVersion, and every msis
// template authors MajorUpgrade with AllowSameVersionUpgrades - which is what lets two builds
// differing only in the fourth field upgrade one another at all. So installing 4.2.0.82 over
// 4.2.0.90 is taken as a same-version upgrade: costing finds the installed, higher-versioned
// binaries and skips the older copies, RemoveExistingProducts then deletes the installed ones,
// and the package reports success with them missing.
//
// The guard makes that a refusal. Each package records its full four-field version, padded so
// that comparing the strings orders the versions, under a registry key named for its
// UpgradeCode. The next package reads it before anything is removed (AppSearch runs before
// LaunchConditions, both long before RemoveExistingProducts) and refuses to install over a
// later one. No custom action is involved; the three fragments go into placeholders every
// MSI template already has.

// guardKey is where a package records its version: one key per UpgradeCode, in the hive view
// of the package's own platform (the component and the search both follow it).
func guardKey(upgradeCode string) string {
	return `Software\msis\Packages\` + strings.ToUpper(strings.Trim(upgradeCode, "{}"))
}

const (
	guardProperty  = "MSIS_INSTALLED_VERSION"
	guardValueName = "Version"
)

// comparableVersion pads each field of a version to five digits and fills missing fields with
// zero: 4.2.0.90 -> 00004.00002.00000.00090. Windows Installer compares two strings
// lexicographically, so on these the comparison is the numeric one, field by field, fourth
// field included. Five digits hold a field's maximum, 65535.
func comparableVersion(version string) (string, error) {
	fields := strings.Split(version, ".")
	if len(fields) > 4 {
		return "", fmt.Errorf("PRODUCT_VERSION %q has more than four fields", version)
	}
	padded := make([]string, 4)
	for i := range padded {
		n := uint64(0)
		if i < len(fields) {
			var err error
			if n, err = strconv.ParseUint(fields[i], 10, 16); err != nil {
				return "", fmt.Errorf("PRODUCT_VERSION %q: field %q is not a number from 0 to 65535", version, fields[i])
			}
		}
		padded[i] = fmt.Sprintf("%05d", n)
	}
	return strings.Join(padded, "."), nil
}

// downgradeGuard returns the guard's three fragments: the search, the launch condition and the
// component recording this package's version. It also files the component under every
// top-level feature, the generated package-items one included, so whichever features an
// install selects, the version is recorded. Call it once every component has its feature. A
// package with no features of its own leaves it to WiX's default feature, as it does every
// component.
//
// A package with no PRODUCT_VERSION gets no guard: WiX refuses such a package anyway.
func (c *Context) downgradeGuard() (search, condition, component string, err error) {
	version := c.Variables.ProductVersion()
	if version == "" {
		return "", "", "", nil
	}
	comparable, err := comparableVersion(version)
	if err != nil {
		return "", "", "", err
	}
	key := guardKey(c.Variables.UpgradeCode())

	search = fmt.Sprintf("        <Property Id='%s'>\n"+
		"            <RegistrySearch Id='%s_SEARCH' Root='HKLM' Key='%s' Name='%s' Type='raw'/>\n"+
		"        </Property>\n", guardProperty, guardProperty, escapeXMLAttr(key), guardValueName)

	// Installed: a repair, modify or uninstall of this very product is never refused. An
	// equal version is let through, as AllowSameVersionUpgrades lets it through today.
	condition = fmt.Sprintf("        <Launch Condition='%s' Message='%s'/>\n",
		escapeXMLAttr(fmt.Sprintf(`Installed OR NOT %s OR %s <= "%s"`, guardProperty, guardProperty, comparable)),
		escapeXMLAttr("A later version of [ProductName] is already installed. To install this "+
			"version, uninstall the installed one first."))

	compID := c.NextComponentID(c.productScopedID("downgrade_guard"))
	component = fmt.Sprintf("        <Component Id='%s' Guid='%s'>\n"+
		"            <RegistryValue Root='HKLM' Key='%s' Name='%s' Type='string' Value='%s' KeyPath='yes'/>\n"+
		"        </Component>\n", compID, GenerateGUID(compID), escapeXMLAttr(key), guardValueName, comparable)
	topLevel := make([]string, 0, len(c.Setup.Features)+1)
	for i := range c.Setup.Features {
		topLevel = append(topLevel, c.featureIDs[fmt.Sprintf("%d", i)])
	}
	// Items written directly under <setup> install through the generated package-items
	// feature, which exists exactly when it has components, and may be all that an install
	// installs (every authored feature disabled).
	if len(c.FeatureComponents[packageItemsFeatureID]) > 0 {
		topLevel = append(topLevel, packageItemsFeatureID)
	}
	for _, featureID := range topLevel {
		c.FeatureComponents[featureID] = append(c.FeatureComponents[featureID], compID)
	}
	return search, condition, component, nil
}
