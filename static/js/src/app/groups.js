var groups = []

// The previous selector escaped the dot in this id, which defeats jQuery's
// getElementById fast path, so it matched every element carrying the id rather
// than the first.
function clearModalFlashes() {
    document.querySelectorAll('[id="modal.flashes"]').forEach(function (container) {
        container.replaceChildren()
    })
}

// targets holds the DataTables instance backing the modal's target list. It is
// recreated every time the modal opens, which is what the destroy option did
// before and is what resets the table's ordering and search between edits.
var targets = null

// Save attempts to POST or PUT to /groups/
function save(id) {
    // Named apart from the module-level DataTables instance, which this
    // function needs to read the rows back out of.
    var targetRecords = []
    targets.rows().data().toArray().forEach(function (target) {
        targetRecords.push({
            first_name: unescapeHtml(target[0]),
            last_name: unescapeHtml(target[1]),
            email: unescapeHtml(target[2]),
            position: unescapeHtml(target[3])
        })
    })
    var group = {
        name: document.getElementById("name").value,
        targets: targetRecords
    }
    // Submit the group
    if (id != -1) {
        // If we're just editing an existing group,
        // we need to PUT /groups/:id
        group.id = id
        api.groupId.put(group)
            .done(function (data) {
                successFlash("Group updated successfully!")
                load()
                dismiss()
                bsModalHide("#modal")
            })
            .fail(function (data) {
                modalError(data.responseJSON.message)
            })
    } else {
        // Else, if this is a new group, POST it
        // to /groups
        api.groups.post(group)
            .done(function (data) {
                successFlash("Group added successfully!")
                load()
                dismiss()
                bsModalHide("#modal")
            })
            .fail(function (data) {
                modalError(data.responseJSON.message)
            })
    }
}

function dismiss() {
    if (targets) {
        targets.clear().draw()
    }
    document.getElementById("name").value = ""
    clearModalFlashes()
}

function edit(id) {
    targets = new DataTable("#targetsTable", {
        destroy: true, // Replace any previously instantiated table
        columnDefs: [{
            orderable: false,
            targets: "no-sort"
        }]
    })
    bindModalSubmit(function () {
        save(id)
    })
    if (id == -1) {
        document.getElementById("groupModalLabel").textContent = "New Group";
        var group = {}
    } else {
        document.getElementById("groupModalLabel").textContent = "Edit Group";
        api.groupId.get(id)
            .done(function (group) {
                document.getElementById("name").value = group.name
                targetRows = []
                group.targets.forEach(function (record) {
                  targetRows.push([
                      escapeHtml(record.first_name),
                      escapeHtml(record.last_name),
                      escapeHtml(record.email),
                      escapeHtml(record.position),
                      '<span style="cursor:pointer;"><i class="fa fa-trash-o"></i></span>'
                  ])
                });
                targets.rows.add(targetRows).draw()
            })
            .fail(function () {
                errorFlash("Error fetching group")
            })
    }
}

function isSupportedCSVFile(file) {
    return /\.(csv|txt)$/i.test(file.name)
}

function uploadCSVFile(file) {
    var formData = new FormData()
    formData.append("files[]", file, file.name)

    return fetch("/api/import/group", {
        method: "POST",
        headers: {
            "Authorization": "Bearer " + user.api_key
        },
        body: formData
    }).then(function (response) {
        return response.json().then(function (result) {
            if (!response.ok) {
                throw new Error(result.message || "Error importing CSV")
            }
            if (!Array.isArray(result)) {
                throw new Error("Invalid CSV import response")
            }
            return result
        }, function () {
            throw new Error("Invalid CSV import response")
        })
    })
}

function addImportedTargets(records) {
    records.forEach(function (record) {
        addTarget(
            record.first_name,
            record.last_name,
            record.email,
            record.position)
    })
    targets.draw()
}

function importCSVFiles(input) {
    var files = Array.prototype.slice.call(input.files)
    clearModalFlashes()

    if (files.length === 0) {
        return
    }

    var supportedFiles = files.filter(isSupportedCSVFile)
    if (supportedFiles.length !== files.length) {
        modalError("Unsupported file extension (use .csv or .txt)")
    }

    supportedFiles.forEach(function (file) {
        uploadCSVFile(file)
            .then(addImportedTargets)
            .catch(function (error) {
                modalError(error instanceof Error ? error.message : "Error importing CSV")
            })
    })
    input.value = ""
}

var downloadCSVTemplate = function () {
    var csvScope = [{
        'First Name': 'Example',
        'Last Name': 'User',
        'Email': 'foobar@example.com',
        'Position': 'Systems Administrator'
    }]
    var filename = 'group_template.csv'
    var csvString = Papa.unparse(csvScope, {})
    var csvData = new Blob([csvString], {
        type: 'text/csv;charset=utf-8;'
    });
    if (navigator.msSaveBlob) {
        navigator.msSaveBlob(csvData, filename);
    } else {
        var csvURL = window.URL.createObjectURL(csvData);
        var dlLink = document.createElement('a');
        dlLink.href = csvURL;
        dlLink.setAttribute('download', filename)
        document.body.appendChild(dlLink)
        dlLink.click();
        document.body.removeChild(dlLink)
    }
}


var deleteGroup = function (id) {
    var group = groups.find(function (x) {
        return x.id === id
    })
    if (!group) {
        return
    }
    Swal.fire({
        title: "Are you sure?",
        text: "This will delete the group. This can't be undone!",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Delete " + escapeHtml(group.name),
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        preConfirm: function () {
            return new Promise(function (resolve, reject) {
                api.groupId.delete(id)
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
                'Group Deleted!',
                'This group has been deleted!',
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

function addTarget(firstNameInput, lastNameInput, emailInput, positionInput) {
    // Create new data row.
    var email = escapeHtml(emailInput).toLowerCase();
    var newRow = [
        escapeHtml(firstNameInput),
        escapeHtml(lastNameInput),
        email,
        escapeHtml(positionInput),
        '<span style="cursor:pointer;"><i class="fa fa-trash-o"></i></span>'
    ];

    // Check table to see if email already exists.
    var targetsTable = targets;
    var existingRowIndex = targetsTable
        .column(2, {
            order: "index"
        }) // Email column has index of 2
        .data()
        .indexOf(email);
    // Update or add new row as necessary.
    if (existingRowIndex >= 0) {
        targetsTable
            .row(existingRowIndex, {
                order: "index"
            })
            .data(newRow);
    } else {
        targetsTable.row.add(newRow);
    }
}

function load() {
    document.getElementById("groupTable").style.display = "none"
    document.getElementById("emptyMessage").style.display = "none"
    document.getElementById("loading").style.display = ""
    api.groups.summary()
        .done(function (response) {
            document.getElementById("loading").style.display = "none"
            if (response.total > 0) {
                groups = response.groups
                document.getElementById("emptyMessage").style.display = "none"
                document.getElementById("groupTable").style.display = ""
                var groupTable = new DataTable("#groupTable", {
                    destroy: true,
                    columnDefs: [{
                        orderable: false,
                        targets: "no-sort"
                    }]
                });
                groupTable.clear();
                groupRows = []
                groups.forEach(function (group) {
                    groupRows.push([
                        escapeHtml(group.name),
                        escapeHtml(group.num_targets),
                        moment(group.modified_date).format('MMMM Do YYYY, h:mm:ss a'),
                        "<div class='float-end'><button class='btn btn-primary' data-bs-toggle='modal' data-bs-target='#modal' onclick='edit(" + group.id + ")'>\
                    <i class='fa fa-pencil'></i>\
                    </button>\
                    <button class='btn btn-danger' onclick='deleteGroup(" + group.id + ")'>\
                    <i class='fa fa-trash-o'></i>\
                    </button></div>"
                    ])
                })
                groupTable.rows.add(groupRows).draw()
            } else {
                document.getElementById("emptyMessage").style.display = ""
            }
        })
        .fail(function () {
            errorFlash("Error fetching groups")
        })
}

// submitHandler holds the listener currently bound to the modal's submit
// button, which is reused for every group.
var submitHandler = null

function bindModalSubmit(handler) {
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
    load()
    // Setup the event listeners
    // Handle manual additions
    document.getElementById("targetForm").addEventListener("submit", function (event) {
        // The previous handler returned false, which cancelled the browser's
        // own submission as well as further propagation.
        event.preventDefault()
        event.stopPropagation()
        // Validate the form data
        var targetForm = document.getElementById("targetForm")
        if (!targetForm.checkValidity()) {
            targetForm.reportValidity()
            return
        }
        addTarget(
            document.getElementById("firstName").value,
            document.getElementById("lastName").value,
            document.getElementById("email").value,
            document.getElementById("position").value);
        targets.draw();

        // Reset user input. The previous selector matched every input directly
        // inside a div child of the form, so the whole set is cleared here too.
        document.querySelectorAll("#targetForm > div > input").forEach(function (input) {
            input.value = ''
        });
        document.getElementById("firstName").focus();
    });
    // Handle Deletion. The row is resolved from the clicked icon with a native
    // DOM lookup, because the table's row selector no longer goes through
    // jQuery.
    document.getElementById("targetsTable").addEventListener("click", function (event) {
        var icon = event.target.closest("span > i.fa-trash-o")
        if (!icon || !targets) {
            return
        }
        var row = icon.closest("tr")
        if (!row) {
            return
        }
        targets.row(row).remove().draw()
    });
    document.getElementById('modal').addEventListener('hide.bs.modal', function () {
        dismiss();
    });
    document.getElementById("csvupload").addEventListener("change", function () {
        importCSVFiles(this)
    })
    document.getElementById("csv-template").addEventListener("click", downloadCSVTemplate)
});
