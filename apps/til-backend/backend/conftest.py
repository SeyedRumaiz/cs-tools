# Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

"""Shared MySQL test-database bootstrap for test_db.py and test_main.py.

Tests run against a real local MySQL database (til_test, on the same
server as the real `til` database) rather than mocks -- a test database
provisioning its own schema is normal test infrastructure, not the app
auto-creating production tables, so this is separate from db.py's own
fail-loud init_db() (which never creates til_submissions -- see
sql/create_til_tables.sql).
"""
import os

import pymysql

os.environ.setdefault("DB_HOST", "localhost")
os.environ.setdefault("DB_PORT", "3306")
os.environ.setdefault("DB_USER", "root")
os.environ.setdefault("DB_PASSWORD", "root")

TEST_DB_NAME = "til_test"
SCHEMA_PATH = os.path.join(os.path.dirname(__file__), "sql", "create_til_tables.sql")


def bootstrap_test_database():
    conn = pymysql.connect(
        host=os.environ["DB_HOST"],
        port=int(os.environ["DB_PORT"]),
        user=os.environ["DB_USER"],
        password=os.environ["DB_PASSWORD"],
    )
    try:
        with conn.cursor() as cur:
            cur.execute(f"CREATE DATABASE IF NOT EXISTS {TEST_DB_NAME} CHARACTER SET utf8mb4")
        conn.select_db(TEST_DB_NAME)
        with open(SCHEMA_PATH) as f:
            # Strip `--` comment lines first -- a stray ';' inside a prose
            # comment (as opposed to real SQL) would otherwise look like a
            # statement boundary to the naive split below.
            schema_sql = "\n".join(
                line for line in f if not line.strip().startswith("--")
            )
        with conn.cursor() as cur:
            for statement in schema_sql.split(";"):
                if statement.strip():
                    cur.execute(statement)
        conn.commit()
    finally:
        conn.close()
