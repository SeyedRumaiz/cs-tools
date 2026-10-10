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

from sanitize import sanitize_what_html, what_for_chat, what_plain_text


def test_keeps_allowlisted_formatting():
    html = "<p>Learned <strong>a lot</strong> from <em>this</em>.</p>"
    assert sanitize_what_html(html) == html


def test_strips_script_tag_and_contents():
    assert sanitize_what_html('<p>hi</p><script>alert("x")</script>') == "<p>hi</p>"


def test_strips_disallowed_attribute_but_keeps_element():
    result = sanitize_what_html('<p onerror="alert(1)">hi</p>')
    assert "onerror" not in result
    assert "hi" in result


def test_keeps_safe_link_href():
    assert 'href="https://example.com"' in sanitize_what_html('<a href="https://example.com">WSO2</a>')


def test_drops_javascript_link():
    assert "javascript:" not in sanitize_what_html('<a href="javascript:alert(1)">bad</a>')


def test_plain_text_counts_only_text_not_markup():
    assert what_plain_text("<p><strong>abc</strong></p>") == "abc"


def test_plain_text_does_not_glue_paragraphs():
    assert what_plain_text("<p>One</p><p>Two</p>") == "One Two"


def test_plain_text_of_empty_quill_markup_is_empty():
    assert what_plain_text("<p><br></p>") == ""


def test_what_for_chat_converts_inline_tags():
    result = what_for_chat("<p>Learned <strong>a lot</strong> from <em>this</em>.</p>")
    assert "<b>a lot</b>" in result
    assert "<i>this</i>" in result
    assert "<p>" not in result and "<strong>" not in result


def test_what_for_chat_converts_list_items():
    result = what_for_chat("<ul><li>First</li><li>Second</li></ul>")
    assert "• First" in result
    assert "• Second" in result
    assert "<ul>" not in result and "<li>" not in result


_UPLOAD_HOST = "localhost:8077"
_UPLOAD_SRC = f"http://{_UPLOAD_HOST}/uploads/07ec86bdee9142da838c9a3511f780e8.webp"


def test_sanitize_allows_img_pointing_at_an_actual_upload():
    result = sanitize_what_html(f'<p>See:</p><img src="{_UPLOAD_SRC}" alt="a screenshot">', request_host=_UPLOAD_HOST)
    assert f'<img src="{_UPLOAD_SRC}" alt="a screenshot">' in result


def test_sanitize_drops_img_onerror_attribute():
    result = sanitize_what_html(f'<img src="{_UPLOAD_SRC}" onerror="alert(1)">', request_host=_UPLOAD_HOST)
    assert "onerror" not in result
    assert "<img" in result


def test_sanitize_keeps_img_width_attribute():
    # Backs the webapp editor's resize overlay (S/M/L width presets) -- a
    # plain dimension attribute, not a CSS "style" string.
    result = sanitize_what_html(f'<img src="{_UPLOAD_SRC}" width="50%">', request_host=_UPLOAD_HOST)
    assert 'width="50%"' in result


def test_sanitize_drops_img_style_attribute():
    # "width" is allowed specifically because it can't carry CSS -- "style"
    # itself must stay out of the allowlist regardless, or a resize overlay
    # could just as easily have been built on an arbitrary style injection
    # surface instead of this one safe attribute.
    result = sanitize_what_html(
        f'<img src="{_UPLOAD_SRC}" style="position:fixed;top:0;left:0;width:100vw;height:100vh;">',
        request_host=_UPLOAD_HOST,
    )
    assert "style" not in result
    assert "<img" in result


def test_sanitize_strips_img_pointing_at_an_arbitrary_external_host():
    # A direct API call (bypassing the editor, which never offers any other
    # image source) could otherwise embed an arbitrary external image --
    # e.g. a tracking pixel that fires whenever any OTHER employee opens
    # the entry. Fails on PATH shape alone here, regardless of host.
    result = sanitize_what_html('<p>See:</p><img src="https://example.com/tracker.png" alt="x">', request_host=_UPLOAD_HOST)
    assert "<img" not in result
    assert "See:" in result


def test_sanitize_strips_img_with_matching_path_on_a_different_host():
    # The gap flagged in review: a PATH that matches this service's own
    # upload shape exactly, but on a HOST that isn't the one the current
    # request actually came in on. Checking path alone (the previous
    # behavior) let this through -- every other employee's browser would
    # fetch the external host's image when they opened the entry.
    evil_src = "https://attacker.example/uploads/07ec86bdee9142da838c9a3511f780e8.webp"
    result = sanitize_what_html(f'<img src="{evil_src}" alt="x">', request_host=_UPLOAD_HOST)
    assert "<img" not in result


def test_sanitize_strips_absolute_img_src_when_request_host_unknown():
    # No request_host passed (the default) means there's nothing to compare
    # against -- treat that as "nothing matches" rather than silently
    # allowing every absolute URL through.
    result = sanitize_what_html(f'<img src="{_UPLOAD_SRC}" alt="x">')
    assert "<img" not in result


def test_sanitize_keeps_relative_upload_src_regardless_of_host():
    # A bare relative path has no host to compare -- same-origin by
    # construction, nothing to validate against request_host.
    result = sanitize_what_html('<img src="/uploads/07ec86bdee9142da838c9a3511f780e8.webp" alt="x">')
    assert "<img" in result


def test_what_for_chat_drops_images_entirely():
    # Images are only ever shown on the entry's own page -- never in the
    # Novera DM broadcast.
    result = what_for_chat(f'<p>Look:</p><img src="{_UPLOAD_SRC}" alt="a screenshot">')
    assert "<img" not in result
    assert "Look" in result
