import urllib.request
import json
import random
import time
import urllib.request
import json
import random
import time
import sys

BASE_URL = "http://localhost:9080/api"

def get_url(endpoint):
    return f"{BASE_URL}{endpoint}"

def get_health_items():
    url = get_url("/health?limit=10000")
    try:
        req = urllib.request.Request(url)
        with urllib.request.urlopen(req) as response:
            if response.status != 200:
                print(f"Error getting health items: {response.status}")
                return []
            data = json.loads(response.read().decode())
            return data.get('data', [])
    except Exception as e:
        print(f"Failed to connect to API: {e}")
        return []

def trigger_check(item_id, file_path):
    url = get_url(f"/health/{item_id}/check-now")
    try:
        req = urllib.request.Request(url, method="POST")
        with urllib.request.urlopen(req) as response:
            if response.status == 200:
                print(f"Triggered check for [{item_id}] {file_path}")
                return True
            else:
                print(f"Failed to trigger check for {file_path}: {response.status}")
                return False
    except urllib.error.HTTPError as e:
        if e.code == 409:
             print(f"Check already in progress for {file_path}")
        else:
             print(f"Error triggering check for {file_path}: {e}")
        return False
    except Exception as e:
        print(f"Error triggering check for {file_path}: {e}")
        return False
    except urllib.error.HTTPError as e:
        if e.code == 409:
             print(f"Check already in progress for {file_path}")
        else:
             print(f"Error triggering check for {file_path}: {e}")
        return False
    except Exception as e:
        print(f"Error triggering check for {file_path}: {e}")
        return False

def main():
    print("Fetching health items...")
    items = get_health_items()
    
    if not items:
        print("No health items found.")
        return

    print(f"Found {len(items)} items.")
    
    # Filter out items that are already checking
    items = [i for i in items if i.get('status') != 'checking']
    
    if not items:
        print("No eligible items found (all might be checking already).")
        return

    count = min(len(items), 100)
    selected_items = random.sample(items, count)
    
    print(f"Selected {len(selected_items)} items for testing.")
    
    triggered_count = 0
    for item in selected_items:
        item_id = item.get('id')
        file_path = item.get('file_path')
        if item_id:
            if trigger_check(item_id, file_path):
                triggered_count += 1
            time.sleep(0.05) 
            
    print(f"Successfully triggered {triggered_count} checks.")

if __name__ == "__main__":
    main()
