import sqlite3
import os

db_path = 'config/altmount.db'
search_term = '%Naughty Ninjas%'

if not os.path.exists(db_path):
    print(f"Database not found at {db_path}")
    exit(1)

try:
    conn = sqlite3.connect(db_path)
    cursor = conn.cursor()

    print("--- media_files ---")
    try:
        cursor.execute("SELECT * FROM media_files WHERE file_path LIKE ?", (search_term,))
        rows = cursor.fetchall()
        if rows:
            for row in rows:
                print(row)
        else:
            print("No matches in media_files")
    except sqlite3.OperationalError as e:
        print(f"Error querying media_files: {e}")

    print("\n--- file_health ---")
    try:
        cursor.execute("SELECT * FROM file_health WHERE file_path LIKE ?", (search_term,))
        rows = cursor.fetchall()
        if rows:
            for row in rows:
                print(row)
        else:
            print("No matches in file_health")
    except sqlite3.OperationalError as e:
        print(f"Error querying file_health: {e}")

    print("\n--- import_queue_items ---")
    try:
        cursor.execute("SELECT * FROM import_queue_items WHERE nzb_path LIKE ? OR storage_path LIKE ?", (search_term, search_term))
        rows = cursor.fetchall()
        if rows:
            for row in rows:
                print(row)
        else:
            print("No matches in import_queue_items")
    except sqlite3.OperationalError as e:
        print(f"Error querying import_queue_items: {e}")

    conn.close()
except Exception as e:
    print(f"An error occurred: {e}")
