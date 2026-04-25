from playwright.sync_api import sync_playwright

BASE_URL = 'http://localhost:9999/wiki'

def test_all():
    errors = []

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()

        # Test 1: Index page loads and has content
        print("Test 1: Index page...")
        page.goto(BASE_URL)
        page.wait_for_load_state('networkidle')

        if page.locator('text=Wiki page not found').is_visible():
            errors.append("Index page shows 'Wiki page not found'")
        else:
            # Check that content exists (use .first to handle multiple h1 elements)
            content = page.locator('h1').first.text_content()
            print(f"  ✓ Index page loaded, title: {content}")

        # Test 2: Click source link from index
        print("\nTest 2: Navigate to source from index...")
        page.goto(BASE_URL)
        page.wait_for_load_state('networkidle')

        # Find source link (e.g., DeepSeek_V4)
        source_link = page.locator('a[href*="sources"]').first
        if source_link.is_visible():
            href = source_link.get_attribute('href')
            print(f"  Found source link: {href}")
            source_link.click()
            page.wait_for_load_state('networkidle')

            if page.locator('text=Wiki page not found').is_visible():
                errors.append(f"Source page '{href}' shows 'Wiki page not found'")
            else:
                print(f"  ✓ Source page loaded: {page.url}")
        else:
            errors.append("No source link found on index page")

        # Test 3: Navigate to topics page
        print("\nTest 3: Topics page and ../entities link...")
        page.goto(f"{BASE_URL}/topics")
        page.wait_for_load_state('networkidle')

        if page.locator('text=Wiki page not found').is_visible():
            errors.append("Topics page shows 'Wiki page not found'")
        else:
            print(f"  ✓ Topics page loaded")

            # Find a link that uses relative path (like ../entities/CSA.md)
            all_links = page.locator('a[href*="wiki"]').all()
            print(f"  Found {len(all_links)} wiki links on topics page")

            for link in all_links[:10]:
                href = link.get_attribute('href')
                if href and 'entities' in href:
                    text = link.text_content()
                    print(f"  Clicking: {text} -> {href}")
                    link.click()
                    page.wait_for_load_state('networkidle')

                    if page.locator('text=Wiki page not found').is_visible():
                        errors.append(f"Entity page '{href}' shows 'Wiki page not found'")
                    else:
                        print(f"  ✓ Entity page loaded: {page.url}")
                    break

        # Test 4: Navigate to entities page
        print("\nTest 4: Entities page and ../sources link...")
        page.goto(f"{BASE_URL}/entities")
        page.wait_for_load_state('networkidle')

        if page.locator('text=Wiki page not found').is_visible():
            errors.append("Entities page shows 'Wiki page not found'")
        else:
            print(f"  ✓ Entities page loaded")

            # Find a source link (like ../sources/DeepSeek_V4.md)
            all_links = page.locator('a[href*="wiki"]').all()
            for link in all_links[:10]:
                href = link.get_attribute('href')
                if href and 'sources' in href:
                    text = link.text_content()
                    print(f"  Clicking: {text} -> {href}")
                    link.click()
                    page.wait_for_load_state('networkidle')

                    if page.locator('text=Wiki page not found').is_visible():
                        errors.append(f"Source page '{href}' shows 'Wiki page not found'")
                    else:
                        print(f"  ✓ Source page loaded: {page.url}")
                    break

        # Test 5: Direct navigation to entity page
        print("\nTest 5: Direct navigation to entity (CSA)...")
        page.goto(f"{BASE_URL}/entities/CSA")
        page.wait_for_load_state('networkidle')

        if page.locator('text=Wiki page not found').is_visible():
            errors.append("Entity CSA page shows 'Wiki page not found'")
        else:
            h1 = page.locator('h1').first.text_content()
            print(f"  ✓ CSA entity page loaded, title: {h1}")

        # Test 6: Direct navigation to topic page
        print("\nTest 6: Direct navigation to topic...")
        page.goto(f"{BASE_URL}/topics/Long-Context-Efficiency")
        page.wait_for_load_state('networkidle')

        if page.locator('text=Wiki page not found').is_visible():
            errors.append("Topic Long-Context-Efficiency shows 'Wiki page not found'")
        else:
            h1 = page.locator('h1').first.text_content()
            print(f"  ✓ Topic page loaded, title: {h1}")

            # Check Related links work
            related_links = page.locator('a[href*="entities"]').all()
            print(f"  Found {len(related_links)} entity links in Related section")

            if related_links:
                link = related_links[0]
                href = link.get_attribute('href')
                text = link.text_content()
                print(f"  Clicking Related link: {text} -> {href}")
                link.click()
                page.wait_for_load_state('networkidle')

                if page.locator('text=Wiki page not found').is_visible():
                    errors.append(f"Related link '{href}' shows 'Wiki page not found'")
                else:
                    print(f"  ✓ Related entity page loaded: {page.url}")

        # Test 7: Quick Links sidebar navigation
        print("\nTest 7: Quick Links sidebar...")
        page.goto(BASE_URL)
        page.wait_for_load_state('networkidle')

        # Check Quick Links section exists
        quick_links_header = page.locator('text=Quick Links')
        if quick_links_header.is_visible():
            print("  ✓ Quick Links section found")

            # Test clicking on a dynamic page link (like DeepSeek_V4)
            # These should use full path from index.md (e.g., sources/DeepSeek_V4)
            sidebar = page.locator('aside').last
            dynamic_links = sidebar.locator('a[href*="/wiki/"]').all()

            # Find a link that should have full path (not just entity/topic name)
            for link in dynamic_links:
                href = link.get_attribute('href')
                text = link.text_content()
                # Skip Index, Entities, Topics, Sources static links
                if text and href and text not in ['Index', 'Entities', 'Topics', 'Sources']:
                    print(f"  Clicking dynamic link: {text} -> {href}")
                    link.click()
                    page.wait_for_load_state('networkidle')

                    if page.locator('text=Wiki page not found').is_visible():
                        errors.append(f"Quick Links dynamic link '{href}' shows 'Wiki page not found'")
                    else:
                        print(f"  ✓ Dynamic page loaded: {page.url}")
                    break
        else:
            errors.append("Quick Links section not found")

        browser.close()

    # Report results
    print("\n" + "="*50)
    if errors:
        print("FAILED - Errors found:")
        for err in errors:
            print(f"  ❌ {err}")
        return False
    else:
        print("✅ ALL TESTS PASSED")
        return True

if __name__ == '__main__':
    success = test_all()
    exit(0 if success else 1)