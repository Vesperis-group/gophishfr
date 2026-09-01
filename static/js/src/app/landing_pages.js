/*
	landing_pages.js
	Handles the creation, editing, and deletion of landing pages
	Author: Jordan Wright <github.com/jordan-wright>
*/
var pages = []
var htmlEditor


// Save attempts to POST to /templates/
function save(idx) {
    var page = {}
    page.name = document.getElementById("name").value
    page.html = htmlEditor.getData()
    page.capture_credentials = document.getElementById("capture_credentials_checkbox").checked
    page.capture_passwords = document.getElementById("capture_passwords_checkbox").checked
    page.redirect_url = document.getElementById("redirect_url_input").value
    if (idx != -1) {
        page.id = pages[idx].id
        api.pageId.put(page)
            .done(function (data) {
                successFlash("Page edited successfully!")
                load()
                dismiss()
            })
    } else {
        // Submit the page
        api.pages.post(page)
            .done(function (data) {
                successFlash("Page added successfully!")
                load()
                dismiss()
            })
            .fail(function (data) {
                modalError(data.responseJSON.message)
            })
    }
}

// The previous selector escaped the dot in this id, which defeats jQuery's
// getElementById fast path, so it matched every element carrying the id rather
// than the first.
function clearModalFlashes() {
    document.querySelectorAll('[id="modal.flashes"]').forEach(function (container) {
        container.replaceChildren()
    })
}

// These two blocks are hidden by the stylesheet rather than by an inline
// style, so revealing them needs an explicit display value: clearing the
// inline one would leave the stylesheet rule in force. This is what jQuery's
// show() resolved to for a div.
function setCredentialOptionsVisible(visible) {
    var display = visible ? "block" : "none"
    document.getElementById("capture_passwords").style.display = display
    document.getElementById("redirect_url").style.display = display
}

function dismiss() {
    clearModalFlashes()
    document.getElementById("name").value = ""
    document.getElementById("html_editor").value = ""
    if (htmlEditor) {
        htmlEditor.setData("")
        htmlEditor.showSource()
    }
    document.getElementById("url").value = ""
    document.getElementById("redirect_url_input").value = ""
    document.querySelectorAll("#modal input[type='checkbox']").forEach(function (box) {
        box.checked = false
    })
    setCredentialOptionsVisible(false)
    bsModalHide("#modal")
}

var deletePage = function (idx) {
    Swal.fire({
        title: "Are you sure?",
        text: "This will delete the landing page. This can't be undone!",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Delete " + escapeHtml(pages[idx].name),
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        preConfirm: function () {
            return new Promise(function (resolve, reject) {
                api.pageId.delete(pages[idx].id)
                    .done(function (msg) {
                        resolve()
                    })
                    .fail(function (data) {
                        reject(data.responseJSON.message)
                    })
            })
        }
    }).then(function (result) {
        if (result.value){
            Swal.fire(
                'Landing Page Deleted!',
                'This landing page has been deleted!',
                'success'
            );
        }
        // Every button whose label contains "OK", which is what the previous
        // selector matched.
        document.querySelectorAll("button").forEach(function (button) {
            if (button.textContent.includes("OK")) {
                button.addEventListener('click', function () {
                    location.reload()
                })
            }
        })
    })
}

function importSite() {
    url = document.getElementById("url").value
    if (!url) {
        modalError("No URL Specified!")
    } else {
        api.clone_site({
                url: url,
                include_resources: false
            })
            .done(function (data) {
                htmlEditor.setData(data.html)
                htmlEditor.showPreview()
                bsModalHide("#importSiteModal")
            })
            .fail(function (data) {
                modalError(data.responseJSON.message)
            })
    }
}

function edit(idx) {
    bindModalSubmit(function () {
        save(idx)
    })
    htmlEditor = GophishHTMLEditor.create("html_editor")
    var page = {}
    if (idx != -1) {
        document.getElementById("modalLabel").textContent = "Edit Landing Page"
        page = pages[idx]
        document.getElementById("name").value = page.name
        htmlEditor.setData(page.html)
        document.getElementById("capture_credentials_checkbox").checked = page.capture_credentials
        document.getElementById("capture_passwords_checkbox").checked = page.capture_passwords
        document.getElementById("redirect_url_input").value = page.redirect_url
        if (page.capture_credentials) {
            setCredentialOptionsVisible(true)
        }
    } else {
        document.getElementById("modalLabel").textContent = "New Landing Page"
        htmlEditor.setData("")
    }
    htmlEditor.showSource()
}

function copy(idx) {
    bindModalSubmit(function () {
        save(-1)
    })
    htmlEditor = GophishHTMLEditor.create("html_editor")
    var page = pages[idx]
    document.getElementById("name").value = "Copy of " + page.name
    htmlEditor.setData(page.html)
    htmlEditor.showSource()
}

function load() {
    /*
        load() - Loads the current pages using the API
    */
    document.getElementById("pagesTable").style.display = "none"
    document.getElementById("emptyMessage").style.display = "none"
    document.getElementById("loading").style.display = ""
    api.pages.get()
        .done(function (ps) {
            pages = ps
            document.getElementById("loading").style.display = "none"
            if (pages.length > 0) {
                document.getElementById("pagesTable").style.display = ""
                pagesTable = new DataTable("#pagesTable", {
                    destroy: true,
                    columnDefs: [{
                        orderable: false,
                        targets: "no-sort"
                    }]
                });
                pagesTable.clear()
                pageRows = []
                pages.forEach(function (page, i) {
                    pageRows.push([
                        escapeHtml(page.name),
                        moment(page.modified_date).format('MMMM Do YYYY, h:mm:ss a'),
                        "<div class='float-end'><span data-bs-toggle='modal' data-bs-target='#modal'><button class='btn btn-primary' data-bs-toggle='tooltip' data-bs-placement='left' title='Edit Page' onclick='edit(" + i + ")'>\
                    <i class='fa fa-pencil'></i>\
                    </button></span>\
		    <span data-bs-toggle='modal' data-bs-target='#modal'><button class='btn btn-primary' data-bs-toggle='tooltip' data-bs-placement='left' title='Copy Page' onclick='copy(" + i + ")'>\
                    <i class='fa fa-copy'></i>\
                    </button></span>\
                    <button class='btn btn-danger' data-bs-toggle='tooltip' data-bs-placement='left' title='Delete Page' onclick='deletePage(" + i + ")'>\
                    <i class='fa fa-trash-o'></i>\
                    </button></div>"
                    ])
                })
                pagesTable.rows.add(pageRows).draw()
                bsInitTooltips()
            } else {
                document.getElementById("emptyMessage").style.display = ""
            }
        })
        .fail(function () {
            document.getElementById("loading").style.display = "none"
            errorFlash("Error fetching pages")
        })
}

// submitHandler holds the listener currently bound to the modal's submit
// button, which is reused for every page.
var submitHandler = null

function bindModalSubmit(handler) {
    // Two elements on this page carry the id "modalSubmit", one per modal. The
    // previous selector took jQuery's getElementById fast path and so bound
    // only the first, which is the page modal's Save button.
    var submit = document.getElementById("modalSubmit")
    if (submitHandler) {
        submit.removeEventListener("click", submitHandler)
    }
    submitHandler = handler
    submit.addEventListener("click", submitHandler)
}

// The application scripts are plain classic scripts at the end of <body>, so
// the document is still parsing when they run and DOMContentLoaded has not
// fired yet.
document.addEventListener('DOMContentLoaded', function () {
    // Modal dismiss handler (native Bootstrap 5 event, no jQuery bridge)
    document.getElementById('modal').addEventListener('hidden.bs.modal', function (event) {
        dismiss()
    });
    document.getElementById("capture_credentials_checkbox").addEventListener("change", function () {
        setCredentialOptionsVisible(this.checked)
    })
    load()
})
